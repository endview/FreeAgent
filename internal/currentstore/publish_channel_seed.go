package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// PublishControlCatalogWithChannelCursorSeedInput closes one enabled Channel
// Endpoint publication and its revision-zero cursor seed. CursorSeed's
// PublishedBasis must be the exact basis produced by Publication. The
// EndpointDisabled assertion belongs only to SeedChannelCursor and is not an
// authority input to this atomic path.
type PublishControlCatalogWithChannelCursorSeedInput struct {
	Publication PublishControlCatalogInput
	CursorSeed  ChannelCursorSeedInput
}

// PublishControlCatalogWithChannelCursorSeed publishes one immutable
// Control/Catalog pair and closes its enabled Channel Endpoint to a verified
// revision-zero CURSOR_SEED in the same BEGIN IMMEDIATE transaction. It never
// creates a mutable cursor head.
func (store *Store) PublishControlCatalogWithChannelCursorSeed(
	ctx context.Context,
	input PublishControlCatalogWithChannelCursorSeedInput,
) (
	controlcontract.PublishedBasis,
	ChannelIngressReceipt,
	error,
) {
	if ctx == nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidPublication,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	defer unlock()

	publication, err := prepareControlCatalogPublication(input.Publication)
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	seed, err := prepareAtomicChannelCursorSeed(publication.basis, input.CursorSeed)
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}

	connection, err := beginChannelIngressTransaction(
		ctx,
		store.db,
		"Control/Catalog publication with cursor seed",
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	defer connection.Close()
	committed := false
	defer rollbackChannelIngress(connection, &committed)

	alreadyPublished, err := publishControlCatalogInTransaction(
		ctx,
		connection,
		publication,
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	if _, _, _, err := verifyChannelEndpointAndAuthority(
		ctx,
		connection,
		publication.control,
		publication.catalog,
		seed.endpointClosure,
		true,
	); err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}

	receipt, found, err := queryChannelReceiptByRevisionRaw(
		ctx,
		connection,
		seed.input.TenantID,
		seed.input.EndpointID,
		seed.input.CursorScopeKey,
		0,
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	if found {
		if err := verifyExactAtomicChannelCursorSeed(
			ctx,
			connection,
			receipt,
			seed,
		); err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		receipt.Created = false
	} else {
		// If the pointer already existed before this call, a missing seed proves
		// that this is not an exact retry of the atomic operation.
		if alreadyPublished {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, fmt.Errorf(
				"%w: exact publication has no revision-zero cursor seed",
				ErrChannelIngressConflict,
			)
		}
		current, currentFound, err := queryCurrentChannelReceipt(
			ctx,
			connection,
			seed.input.TenantID,
			seed.input.EndpointID,
			seed.input.CursorScopeKey,
		)
		if err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		if currentFound {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, fmt.Errorf(
				"%w: cursor scope has revision %d without an intact seed",
				ErrChannelIngressConflict,
				current.CursorRevision,
			)
		}

		createdAt := nowUnixMicro()
		if createdAt <= 0 {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, fmt.Errorf(
				"%w: invalid cursor seed time",
				ErrChannelIngressIntegrity,
			)
		}
		if err := putAdmissionContent(
			ctx,
			connection,
			seed.cursorAfter,
			createdAt,
		); err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		requested := newAtomicChannelCursorSeedReceipt(seed)
		if err := insertChannelReceipt(
			ctx,
			connection,
			requested,
			createdAt,
		); err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		receipt, err = loadChannelReceiptByRevision(
			ctx,
			connection,
			seed.input.TenantID,
			seed.input.EndpointID,
			seed.input.CursorScopeKey,
			0,
		)
		if err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		if err := verifyExactAtomicChannelCursorSeed(
			ctx,
			connection,
			receipt,
			seed,
		); err != nil {
			return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
		}
		receipt.Created = true
	}

	if err := commitChannelIngressTransaction(
		ctx,
		connection,
		"Control/Catalog publication with cursor seed",
	); err != nil {
		return controlcontract.PublishedBasis{}, ChannelIngressReceipt{}, err
	}
	committed = true
	return publication.basis, receipt, nil
}

type preparedAtomicChannelCursorSeed struct {
	input           ChannelCursorSeedInput
	cursorAfter     preparedAdmissionContent
	endpointClosure preparedChannelIngressEvent
}

func prepareAtomicChannelCursorSeed(
	basis controlcontract.PublishedBasis,
	input ChannelCursorSeedInput,
) (preparedAtomicChannelCursorSeed, error) {
	if input.PublishedBasis != basis {
		return preparedAtomicChannelCursorSeed{}, fmt.Errorf(
			"%w: cursor seed PublishedBasis does not equal publication basis",
			ErrInvalidChannelIngress,
		)
	}
	if err := validateChannelBasisAndIdentity(
		basis,
		input.TenantID,
		input.WorkspaceID,
		input.EndpointID,
		input.CursorScopeKey,
	); err != nil {
		return preparedAtomicChannelCursorSeed{}, err
	}
	if !moduleapi.ValidSHA256(input.EndpointBindingDigest) {
		return preparedAtomicChannelCursorSeed{}, fmt.Errorf(
			"%w: invalid Endpoint Binding digest",
			ErrInvalidChannelIngress,
		)
	}
	if err := validateChannelReason(input.Reason); err != nil {
		return preparedAtomicChannelCursorSeed{}, err
	}
	cursorAfter, err := prepareChannelContent(
		input.CursorAfter,
		ContentChannelCursor,
		channelCursorMediaType,
		maxChannelCursorBytes,
		true,
	)
	if err != nil {
		return preparedAtomicChannelCursorSeed{}, err
	}
	return preparedAtomicChannelCursorSeed{
		input:       input,
		cursorAfter: cursorAfter,
		endpointClosure: preparedChannelIngressEvent{
			input: ChannelIngressEventInput{
				PublishedBasis:        basis,
				TenantID:              input.TenantID,
				WorkspaceID:           input.WorkspaceID,
				EndpointID:            input.EndpointID,
				CursorScopeKey:        input.CursorScopeKey,
				EndpointBindingDigest: input.EndpointBindingDigest,
			},
		},
	}, nil
}

func newAtomicChannelCursorSeedReceipt(
	seed preparedAtomicChannelCursorSeed,
) ChannelIngressReceipt {
	return ChannelIngressReceipt{
		TenantID:              seed.input.TenantID,
		WorkspaceID:           seed.input.WorkspaceID,
		EndpointID:            seed.input.EndpointID,
		CursorScopeKey:        seed.input.CursorScopeKey,
		CursorRevision:        0,
		CursorAfterRef:        seed.cursorAfter.Digest,
		EndpointBindingDigest: seed.input.EndpointBindingDigest,
		Disposition:           ChannelCursorSeed,
		Reason:                seed.input.Reason,
	}
}

func verifyExactAtomicChannelCursorSeed(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	receipt ChannelIngressReceipt,
	seed preparedAtomicChannelCursorSeed,
) error {
	if err := verifyChannelReceipt(ctx, queryer, receipt); err != nil {
		return err
	}
	want := newAtomicChannelCursorSeedReceipt(seed)
	if receipt.TenantID != want.TenantID ||
		receipt.WorkspaceID != want.WorkspaceID ||
		receipt.EndpointID != want.EndpointID ||
		receipt.CursorScopeKey != want.CursorScopeKey ||
		receipt.CursorRevision != 0 ||
		receipt.CursorBeforeRef != "" ||
		receipt.CursorAfterRef != want.CursorAfterRef ||
		receipt.EndpointBindingDigest != want.EndpointBindingDigest ||
		receipt.Disposition != ChannelCursorSeed ||
		receipt.Reason != want.Reason {
		return fmt.Errorf(
			"%w: revision-zero cursor seed differs",
			ErrChannelIngressConflict,
		)
	}
	content, err := queryContent(ctx, queryer, receipt.CursorAfterRef)
	if err != nil || content.Kind != seed.cursorAfter.Kind ||
		content.MediaType != seed.cursorAfter.MediaType ||
		content.SizeBytes != int64(len(seed.cursorAfter.CanonicalBytes)) ||
		!bytes.Equal(content.CanonicalBytes, seed.cursorAfter.CanonicalBytes) {
		return fmt.Errorf(
			"%w: revision-zero cursor content differs",
			ErrChannelIngressConflict,
		)
	}
	return nil
}
