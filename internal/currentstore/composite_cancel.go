package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

var (
	ErrInvalidRunCancellation = errors.New(
		"currentstore: invalid Run cancellation",
	)
	ErrRunCanceled = errors.New(
		"currentstore: Run is canceled",
	)
	ErrRunCancellationConflict = errors.New(
		"currentstore: Run cancellation conflict",
	)
)

type RequestRunCancellationInput struct {
	Canonical []byte
}

type RunCancellationResult struct {
	Request corecontract.RunCancellationRequestV1
	Ref     string
	Created bool
}

// RequestRunCancellation installs one immutable, monotonic cancellation latch.
// Scope run is valid only for an ordinary Run and updates that Run alone.
// Scope family is valid only for a composite root and atomically updates the
// root plus every Child frozen in its manifest. A Child's inherited scope can
// never be addressed directly. The operation does not rewrite a PENDING or
// UNKNOWN Attempt or advance a Run/Frame revision: already-authorized work
// remains reconcilable, while every later permit is denied.
func (store *Store) RequestRunCancellation(
	ctx context.Context,
	input RequestRunCancellationInput,
) (RunCancellationResult, error) {
	if ctx == nil {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidRunCancellation,
		)
	}
	canonical := bytes.Clone(input.Canonical)
	request, err := corecontract.RestoreRunCancellationRequestV1(canonical)
	if err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: %v", ErrInvalidRunCancellation, err,
		)
	}
	ref, err := ComputeContentDigest(
		ContentRunCancellation,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: %v", ErrInvalidRunCancellation, err,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return RunCancellationResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"currentstore: acquire Run cancellation connection: %w", err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"currentstore: begin Run cancellation: %w", err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	manifest, err := loadCancellationTargetManifest(
		ctx,
		connection,
		request.RootRunID,
	)
	if err != nil {
		return RunCancellationResult{}, err
	}
	if manifest.ManifestDigest != request.RootManifestDigest {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: root Manifest digest does not match",
			ErrInvalidRunCancellation,
		)
	}

	var members []runCancellationLatchRow
	switch request.Scope {
	case corecontract.CancellationScopeRunV1:
		if manifest.Composite != nil ||
			manifest.CancellationScope != corecontract.CancellationScopeRunV1 {
			return RunCancellationResult{}, fmt.Errorf(
				"%w: scope run requires an ordinary Run",
				ErrInvalidRunCancellation,
			)
		}
		member, loadErr := loadRunCancellationLatchRow(
			ctx,
			connection,
			manifest.RunID,
		)
		if loadErr != nil {
			return RunCancellationResult{}, loadErr
		}
		members = []runCancellationLatchRow{member}
	case corecontract.CancellationScopeFamilyV1:
		if manifest.Composite == nil ||
			manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
			manifest.Composite.Plan == nil ||
			manifest.CancellationScope != corecontract.CancellationScopeFamilyV1 {
			return RunCancellationResult{}, fmt.Errorf(
				"%w: scope family requires an intact composite root",
				ErrInvalidRunCancellation,
			)
		}
		members, err = loadCompositeFamilyLatchRows(ctx, connection, manifest)
		if err != nil {
			return RunCancellationResult{}, err
		}
	default:
		return RunCancellationResult{}, fmt.Errorf(
			"%w: unsupported scope",
			ErrInvalidRunCancellation,
		)
	}

	allEmpty := true
	allExact := true
	for _, member := range members {
		allEmpty = allEmpty && !member.cancelRef.Valid
		allExact = allExact && member.cancelRef.Valid && member.cancelRef.String == ref
	}
	if allExact {
		if err := verifyRunCancellationContent(
			ctx, connection, ref, canonical, request,
		); err != nil {
			return RunCancellationResult{}, err
		}
		for _, member := range members {
			if err := verifyCurrentRunObservationV1(ctx, connection, member.runID); err != nil {
				return RunCancellationResult{}, err
			}
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return RunCancellationResult{}, fmt.Errorf(
				"currentstore: commit idempotent Run cancellation: %w", err,
			)
		}
		committed = true
		return RunCancellationResult{
			Request: request,
			Ref:     ref,
			Created: false,
		}, nil
	}
	if !allEmpty {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: target already contains another or partial cancellation latch",
			ErrRunCancellationConflict,
		)
	}

	createdAt := nowUnixMicro()
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest:         ref,
		Kind:           ContentRunCancellation,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: canonical,
	}, createdAt); err != nil {
		return RunCancellationResult{}, err
	}

	var result sql.Result
	if request.Scope == corecontract.CancellationScopeRunV1 {
		result, err = connection.ExecContext(ctx, `
			UPDATE runs
			SET cancel_request_ref=?, updated_at=?
			WHERE run_id=?
			  AND parent_run_id IS NULL
			  AND cancel_request_ref IS NULL
		`, ref, createdAt, manifest.RunID)
	} else {
		result, err = connection.ExecContext(ctx, `
			UPDATE runs
			SET cancel_request_ref=?, updated_at=?
			WHERE (run_id=? OR parent_run_id=?)
			  AND cancel_request_ref IS NULL
		`, ref, createdAt, manifest.RunID, manifest.RunID)
	}
	if err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"currentstore: install Run cancellation latch: %w", err,
		)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil || affected != int64(len(members)) {
		return RunCancellationResult{}, fmt.Errorf(
			"%w: cancellation latch affected %d rows, want %d",
			ErrRunCancellationConflict,
			affected,
			len(members),
		)
	}
	for _, member := range members {
		if err := appendCancellationRunObservationV1(
			ctx, connection, member.runID,
		); err != nil {
			return RunCancellationResult{}, err
		}
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunCancellationResult{}, fmt.Errorf(
			"currentstore: commit Run cancellation: %w", err,
		)
	}
	committed = true
	return RunCancellationResult{
		Request: request,
		Ref:     ref,
		Created: true,
	}, nil
}

type runCancellationLatchRow struct {
	runID     string
	cancelRef sql.NullString
}

func loadCancellationTargetManifest(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (corecontract.RunManifest, error) {
	var (
		canonical []byte
		digest    string
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(&canonical, &digest); err != nil {
		return corecontract.RunManifest{}, fmt.Errorf(
			"%w: load target Manifest: %v",
			ErrInvalidRunCancellation,
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(canonical)
	if err != nil || manifest.RunID != runID || manifest.ManifestDigest != digest {
		return corecontract.RunManifest{}, fmt.Errorf(
			"%w: Run %q does not have an intact Manifest",
			ErrInvalidRunCancellation,
			runID,
		)
	}
	return manifest, nil
}

func loadCompositeRootManifest(
	ctx context.Context,
	connection readQueryerV1,
	rootRunID string,
) (corecontract.RunManifest, error) {
	manifest, err := loadCancellationTargetManifest(ctx, connection, rootRunID)
	if err != nil {
		return corecontract.RunManifest{}, err
	}
	if manifest.Composite == nil ||
		manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		manifest.Composite.Plan == nil ||
		manifest.CancellationScope != corecontract.CancellationScopeFamilyV1 {
		return corecontract.RunManifest{}, fmt.Errorf(
			"%w: Run %q is not an intact composite root",
			ErrInvalidRunCancellation,
			rootRunID,
		)
	}
	return manifest, nil
}

func loadRunCancellationLatchRow(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
) (runCancellationLatchRow, error) {
	var row runCancellationLatchRow
	if err := connection.QueryRowContext(ctx, `
		SELECT run_id, cancel_request_ref
		FROM runs
		WHERE run_id=?
	`, runID).Scan(&row.runID, &row.cancelRef); err != nil {
		return runCancellationLatchRow{}, fmt.Errorf(
			"%w: load Run latch: %v",
			ErrInvalidRunCancellation,
			err,
		)
	}
	return row, nil
}

func loadCompositeFamilyLatchRows(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
) ([]runCancellationLatchRow, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT run_id, cancel_request_ref
		FROM runs
		WHERE run_id=? OR parent_run_id=?
		ORDER BY run_id
	`, manifest.RunID, manifest.RunID)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: load composite cancellation family: %w", err,
		)
	}
	defer rows.Close()
	loaded := make(
		[]runCancellationLatchRow,
		0,
		compositeFamilyRunCount(manifest.Composite.Plan),
	)
	seen := make(map[string]struct{}, cap(loaded))
	for rows.Next() {
		var row runCancellationLatchRow
		if err := rows.Scan(&row.runID, &row.cancelRef); err != nil {
			return nil, fmt.Errorf(
				"currentstore: scan composite cancellation family: %w", err,
			)
		}
		loaded = append(loaded, row)
		seen[row.runID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: iterate composite cancellation family: %w", err,
		)
	}
	if len(loaded) != compositeFamilyRunCount(manifest.Composite.Plan) {
		return nil, fmt.Errorf(
			"%w: persisted family cardinality differs from root plan",
			ErrAdmissionIntegrity,
		)
	}
	if _, ok := seen[manifest.RunID]; !ok {
		return nil, fmt.Errorf(
			"%w: composite root is absent from its family",
			ErrAdmissionIntegrity,
		)
	}
	for _, child := range manifest.Composite.Plan.Children {
		if _, ok := seen[child.RunID]; !ok {
			return nil, fmt.Errorf(
				"%w: composite Child %q is absent",
				ErrAdmissionIntegrity,
				child.RunID,
			)
		}
	}
	if reviewer := manifest.Composite.Plan.Reviewer; reviewer != nil {
		if _, ok := seen[reviewer.RunID]; !ok {
			return nil, fmt.Errorf(
				"%w: composite Reviewer %q is absent",
				ErrAdmissionIntegrity,
				reviewer.RunID,
			)
		}
	}
	if decision := manifest.Composite.Plan.Decision; decision != nil {
		for _, child := range decision.RepairChildren {
			if _, ok := seen[child.RunID]; !ok {
				return nil, fmt.Errorf(
					"%w: composite repair Child %q is absent",
					ErrAdmissionIntegrity,
					child.RunID,
				)
			}
		}
		if _, ok := seen[decision.RepairReviewer.RunID]; !ok {
			return nil, fmt.Errorf(
				"%w: composite repair Reviewer %q is absent",
				ErrAdmissionIntegrity,
				decision.RepairReviewer.RunID,
			)
		}
	}
	return loaded, nil
}

func compositeFamilyRunCount(plan *corecontract.CompositeRunPlanV1) int {
	if plan == nil {
		return 0
	}
	count := len(plan.Children) + 1
	if plan.Reviewer != nil {
		count++
	}
	if plan.Decision != nil {
		count += len(plan.Decision.RepairChildren) + 1
	}
	return count
}

func verifyRunCancellationContent(
	ctx context.Context,
	connection readQueryerV1,
	ref string,
	canonical []byte,
	request corecontract.RunCancellationRequestV1,
) error {
	record, err := queryContent(ctx, connection, ref)
	if err != nil || record.Kind != ContentRunCancellation ||
		record.MediaType != admissionJSONMediaType ||
		!bytes.Equal(record.CanonicalBytes, canonical) {
		return fmt.Errorf(
			"%w: cancellation content does not close",
			ErrAdmissionIntegrity,
		)
	}
	computed, err := ComputeContentDigest(
		record.Kind,
		record.MediaType,
		record.CanonicalBytes,
	)
	if err != nil || computed != ref {
		return fmt.Errorf(
			"%w: cancellation content digest differs",
			ErrAdmissionIntegrity,
		)
	}
	restored, err := corecontract.RestoreRunCancellationRequestV1(
		record.CanonicalBytes,
	)
	if err != nil || restored != request {
		return fmt.Errorf(
			"%w: cancellation content is not the requested latch",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}
