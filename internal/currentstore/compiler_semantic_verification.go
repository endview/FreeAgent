package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyConversationCompilerOutputsV1 deterministically recompiles every
// Context-Compiler-owned model request in a Conversation. It is a read-only
// semantic-verification entry used by backup/restore after the ordinary Store
// and content-address closures have passed. No Provider, current Control state,
// or mutable Memory head is consulted.
//
// Action model-2 requests are deliberately excluded: they are derived from the
// frozen Action result by their separate request-closure validator, not by the
// Context Compiler.
func VerifyConversationCompilerOutputsV1(
	ctx context.Context,
	connection *sql.Conn,
) error {
	if ctx == nil || connection == nil {
		return fmt.Errorf("conversation compiler verification requires a context and connection")
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt.attempt_id
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS run ON run.run_id=attempt.run_id
		WHERE run.conversation_id IS NOT NULL
		  AND attempt.source_dispatch_attempt_id IS NULL
		ORDER BY run.conversation_turn_index, attempt.created_at, attempt.attempt_id
	`)
	if err != nil {
		return fmt.Errorf("read Conversation compiler Attempts: %w", err)
	}
	attemptIDs := make([]string, 0)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan Conversation compiler Attempt: %w", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate Conversation compiler Attempts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close Conversation compiler Attempts: %w", err)
	}

	for _, attemptID := range attemptIDs {
		dispatch, err := queryModelDispatchRecord(ctx, connection, attemptID)
		if err != nil {
			return fmt.Errorf("Conversation compiler Attempt %q: %w", attemptID, err)
		}
		request, err := moduleapi.RestoreModelGenerateRequestV1(
			dispatch.Attempt.Request.CanonicalBytes,
		)
		if err != nil {
			return fmt.Errorf("Conversation compiler Attempt %q request: %w", attemptID, err)
		}

		var compilation corecontract.ContextCompilationV1
		var compilationCanonical []byte
		if dispatch.Attempt.ContextCompilation != nil {
			compilationCanonical = bytes.Clone(
				dispatch.Attempt.ContextCompilation.CanonicalBytes,
			)
			compilation, err = corecontract.RestoreContextCompilationV1(
				compilationCanonical,
			)
			if err != nil {
				return fmt.Errorf(
					"Conversation compiler Attempt %q compilation: %w",
					attemptID,
					err,
				)
			}
		}

		run, err := loadConversationCompilerRunV1(
			ctx,
			connection,
			dispatch.Attempt.RunID,
			compilation,
		)
		if err != nil {
			return fmt.Errorf(
				"Conversation compiler Attempt %q frozen Run: %w",
				attemptID,
				err,
			)
		}
		knowledgeBindings, err := frozenKnowledgeBindingsForRun(
			run.Manifest,
			run.Member,
			runKnowledgeContentGetter(run),
		)
		if err != nil {
			return fmt.Errorf(
				"Conversation compiler Attempt %q Knowledge closure: %w",
				attemptID,
				err,
			)
		}
		memoryBindings, err := frozenMemoryBindingsForRun(
			run.Manifest,
			run.Member,
			runKnowledgeContentGetter(run),
		)
		if err != nil {
			return fmt.Errorf(
				"Conversation compiler Attempt %q Memory closure: %w",
				attemptID,
				err,
			)
		}
		if err := validateCompilerOutputForNewAttempt(
			compilation,
			compilationCanonical,
			run,
			request,
			dispatch.Attempt.FrameRevision,
			knowledgeBindings,
			memoryBindings,
		); err != nil {
			return fmt.Errorf(
				"Conversation compiler Attempt %q exact output: %w",
				attemptID,
				err,
			)
		}
	}
	return nil
}

func loadConversationCompilerRunV1(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
	compilation corecontract.ContextCompilationV1,
) (RunForLoop, error) {
	manifest, member, err := loadConversationRunIdentity(ctx, connection, runID)
	if err != nil {
		return RunForLoop{}, err
	}
	if manifest.ConversationTurn == nil {
		return RunForLoop{}, ErrAdmissionIntegrity
	}
	contents, err := loadLoopRecoveryContents(ctx, connection, manifest, member)
	if err != nil {
		return RunForLoop{}, err
	}
	for _, evidence := range compilation.MemoryReads {
		record, err := queryContent(
			ctx,
			connection,
			evidence.Snapshot.Digest,
		)
		if err != nil || record.Kind != ContentMemorySnapshot {
			return RunForLoop{}, loopReadIntegrity(
				"Conversation compiler Memory snapshot",
				err,
			)
		}
		contents = appendCompilerVerificationContent(contents, record)
	}
	conversationHistory, err := loadLoopConversationHistory(
		ctx,
		connection,
		manifest,
		member,
	)
	if err != nil {
		return RunForLoop{}, err
	}

	run := RunForLoop{
		RunID:               runID,
		Manifest:            manifest,
		Member:              member,
		Contents:            contents,
		ConversationHistory: conversationHistory,
	}
	if manifest.Composite == nil {
		return run, nil
	}
	switch manifest.Composite.Role {
	case corecontract.CompositeRunRoleChildV1:
		return run, nil
	case corecontract.CompositeRunRoleRootV1:
		children, err := loadCompositeChildResults(ctx, connection, manifest)
		if err != nil {
			return RunForLoop{}, err
		}
		reviewer, err := loadCompositeReviewerResult(
			ctx,
			connection,
			manifest,
			children,
		)
		if err != nil {
			return RunForLoop{}, err
		}
		run.CompositeChildren = children
		run.CompositeReviewer = reviewer
		return run, nil
	case corecontract.CompositeRunRoleReviewerV1:
		root, err := loadCompositeRootManifest(
			ctx,
			connection,
			manifest.Composite.RootRunID,
		)
		if err != nil || manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
			validateCompositeReviewerRunAgainstRoot(root, manifest, member) != nil {
			return RunForLoop{}, loopReadIntegrity(
				"Conversation compiler Reviewer root",
				err,
			)
		}
		children, err := loadCompositeChildResults(ctx, connection, root)
		if err != nil {
			return RunForLoop{}, err
		}
		run.CompositeRoot = &root
		run.CompositeChildren = children
		return run, nil
	default:
		return RunForLoop{}, ErrAdmissionIntegrity
	}
}

func appendCompilerVerificationContent(
	contents []ContentRecord,
	record ContentRecord,
) []ContentRecord {
	index := sort.Search(len(contents), func(index int) bool {
		return contents[index].Digest >= record.Digest
	})
	if index < len(contents) && contents[index].Digest == record.Digest {
		return contents
	}
	contents = append(contents, ContentRecord{})
	copy(contents[index+1:], contents[index:])
	contents[index] = cloneContentRecord(record)
	return contents
}
