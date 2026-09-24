package coreloop

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// prepareModelAfterActionRequestV1 deterministically derives model two from
// persisted facts only. It never reruns Context Compiler or a Context/RAG/
// Memory provider: the sole mutation is one exact untrusted Action result
// message appended to model one's already-frozen request.
func prepareModelAfterActionRequestV1(
	run currentstore.RunForLoop,
) (preparedPureChatRequestV1, error) {
	if run.Frame.Step != corecontract.ModelReadyAfterActionLoopStep {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: Run is not ready for model two",
			ErrInvalidPureChatRequest,
		)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continued.AttemptKind != corecontract.AttemptKindAction ||
		continued.AttemptID == "" {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-two continuation does not name its Action source",
			ErrInvalidPureChatRequest,
		)
	}
	action, err := exactActionDispatchForModelTwo(run, continued.AttemptID)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	definition, err := exactActionDefinitionForModelTwo(run, action)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	if action.Result == nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: successful Action has no result content",
			ErrInvalidPureChatRequest,
		)
	}
	result, err := corecontract.RestoreActionResultV1(
		action.Result.CanonicalBytes,
		action.Result.Digest,
		definition,
	)
	if err != nil || result.Status != corecontract.ActionResultAvailable {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: Action result is not exact AVAILABLE content",
			ErrInvalidPureChatRequest,
		)
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		result,
		definition,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: construct Action result envelope: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	source, err := exactSourceModelForAction(run, action)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		source.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: restore model-one request: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	if len(request.Messages) >= moduleapi.MaxManifestEntries {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-one request did not reserve an Action result message slot",
			ErrInvalidPureChatRequest,
		)
	}
	request.Messages = append(
		append([]moduleapi.ModelMessageV1{}, request.Messages...),
		envelope,
	)
	frozen, canonical, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: freeze model-two request: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	if source.Attempt.ContextCompilation == nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: Action-enabled model one lacks context compilation",
			ErrInvalidPureChatRequest,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		source.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || compilation.ActionResultReservation == nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-one Action reservation is absent or invalid",
			ErrInvalidPureChatRequest,
		)
	}
	requestDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelRequest,
		"application/json",
		source.Attempt.Request.CanonicalBytes,
	)
	if err != nil || compilation.FinalRequestDigest != requestDigest {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-one compilation does not close its request",
			ErrInvalidPureChatRequest,
		)
	}
	wantReservation, err := corecontract.NewActionResultReservationV1(
		run.Member.Actions,
	)
	if err != nil || *compilation.ActionResultReservation != wantReservation {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-one Action reservation differs from the frozen member",
			ErrInvalidPureChatRequest,
		)
	}
	modelTwoEstimate, err := contextcompiler.EstimateModelGenerateRequestV1(frozen)
	if err != nil || modelTwoEstimate > compilation.FinalEstimateTokens {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: model-two request exceeds the persisted Action reservation",
			ErrInvalidPureChatRequest,
		)
	}
	return preparedPureChatRequestV1{
		Request:          frozen,
		RequestCanonical: bytes.Clone(canonical),
	}, nil
}

func exactActionDispatchForModelTwo(
	run currentstore.RunForLoop,
	attemptID string,
) (currentstore.ActionDispatchRecord, error) {
	var found *currentstore.ActionDispatchRecord
	for index := range run.ActionDispatches {
		candidate := &run.ActionDispatches[index]
		if candidate.Attempt.AttemptID == attemptID {
			if found != nil {
				return currentstore.ActionDispatchRecord{}, fmt.Errorf(
					"%w: duplicate Action source",
					ErrInvalidPureChatRequest,
				)
			}
			found = candidate
		}
	}
	if found == nil ||
		found.Attempt.State != currentstore.ActionDispatchSucceeded ||
		found.Attempt.ResultRef == "" ||
		found.Attempt.UsageLedgerRef != run.Frame.UsageLedgerRef {
		return currentstore.ActionDispatchRecord{}, fmt.Errorf(
			"%w: continuation Action is not a successful result source",
			ErrInvalidPureChatRequest,
		)
	}
	return *found, nil
}

func exactActionDefinitionForModelTwo(
	run currentstore.RunForLoop,
	action currentstore.ActionDispatchRecord,
) (corecontract.FrozenActionDefinitionV1, error) {
	for _, definition := range run.Member.Actions {
		if definition.PublicActionID == action.Attempt.PublicActionID {
			if definition.ProviderActionID != action.Attempt.ProviderActionID ||
				definition.BindingIndex != action.Attempt.BindingIndex ||
				definition.DefinitionDigest != action.Attempt.DefinitionDigest ||
				definition.EffectClass != action.Attempt.EffectClass ||
				definition.MaxResultBytes != action.Attempt.MaxResultBytes {
				break
			}
			return definition, nil
		}
	}
	return corecontract.FrozenActionDefinitionV1{}, fmt.Errorf(
		"%w: Action source differs from the frozen definition",
		ErrInvalidPureChatRequest,
	)
}

func exactSourceModelForAction(
	run currentstore.RunForLoop,
	action currentstore.ActionDispatchRecord,
) (currentstore.ModelDispatchRecord, error) {
	var found *currentstore.ModelDispatchRecord
	for index := range run.ModelDispatches {
		candidate := &run.ModelDispatches[index]
		if candidate.Attempt.AttemptID == action.Attempt.SourceModelAttemptID {
			if found != nil {
				return currentstore.ModelDispatchRecord{}, fmt.Errorf(
					"%w: duplicate source Model Attempt",
					ErrInvalidPureChatRequest,
				)
			}
			found = candidate
		}
	}
	if found == nil || found.Attempt.State != corecontract.ModelAttemptSucceeded ||
		found.Attempt.SourceDispatchAttemptID != "" {
		return currentstore.ModelDispatchRecord{}, fmt.Errorf(
			"%w: Action source Model Attempt is absent or invalid",
			ErrInvalidPureChatRequest,
		)
	}
	return *found, nil
}
