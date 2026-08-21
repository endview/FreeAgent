package modulehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrInvalidPreparedActionExecution = errors.New(
	"modulehost: invalid prepared Action execution",
)

const actionContentRecordDigestDomainV1 = "freeagent.content-record/v1\x00"

var actionProviderPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}

// PreparedActionExecutionV1 is the private, immutable execution closure built
// by Action Gateway only after it consumes the persisted one-shot permit and
// rechecks the exact current activation. ConfigCanonical and
// AuthorityCanonical are the content-addressed bodies referenced by Binding;
// they are carried to the executor so an artifact-scoped adapter never needs
// to cache Instance-specific configuration or authority.
type PreparedActionExecutionV1 struct {
	Request            moduleapi.ActionExecutionRequestV1
	Binding            moduleapi.PortBinding
	ConfigCanonical    json.RawMessage
	AuthorityCanonical json.RawMessage
}

// NewPreparedActionExecutionV1 validates the complete private closure and
// returns a detached copy. It deliberately verifies both content references
// again at the Gateway/Host boundary rather than trusting caller-owned bytes.
func NewPreparedActionExecutionV1(
	input PreparedActionExecutionV1,
) (PreparedActionExecutionV1, error) {
	request, _, err := moduleapi.NewActionExecutionRequestV1(input.Request)
	if err != nil {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: request: %v",
			ErrInvalidPreparedActionExecution,
			err,
		)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     actionProviderPortV1,
		Bindings: []moduleapi.PortBinding{input.Binding},
	})
	if err != nil {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: Binding: %v",
			ErrInvalidPreparedActionExecution,
			err,
		)
	}
	binding := plan.Bindings[0]
	configCanonical := bytes.Clone(input.ConfigCanonical)
	authorityCanonical := bytes.Clone(input.AuthorityCanonical)
	config, err := moduleapi.RestoreActionBindingConfigV1(configCanonical)
	if err != nil {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: ConfigCanonical: %v",
			ErrInvalidPreparedActionExecution,
			err,
		)
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityCanonical,
	)
	if err != nil {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: AuthorityCanonical: %v",
			ErrInvalidPreparedActionExecution,
			err,
		)
	}
	if actionContentRecordDigestV1("CONFIG", configCanonical) !=
		binding.ConfigRef {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: ConfigCanonical does not match Binding.ConfigRef",
			ErrInvalidPreparedActionExecution,
		)
	}
	if actionContentRecordDigestV1("AUTHORITY_CEILING", authorityCanonical) !=
		binding.AuthorityCeilingRef {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: AuthorityCanonical does not match Binding.AuthorityCeilingRef",
			ErrInvalidPreparedActionExecution,
		)
	}
	if !actionConfigClosesRequestV1(config, request) {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: ConfigCanonical does not close the exact Action request",
			ErrInvalidPreparedActionExecution,
		)
	}
	if !containsPreparedActionProviderV1(
		authority.AllowedProviderActionIDs,
		request.ProviderActionID,
	) || request.MaxResultBytes > authority.MaxResultBytes {
		return PreparedActionExecutionV1{}, fmt.Errorf(
			"%w: AuthorityCanonical does not close the exact Action request",
			ErrInvalidPreparedActionExecution,
		)
	}
	return PreparedActionExecutionV1{
		Request:            request,
		Binding:            binding,
		ConfigCanonical:    configCanonical,
		AuthorityCanonical: authorityCanonical,
	}, nil
}

func actionConfigClosesRequestV1(
	config moduleapi.ActionBindingConfigV1,
	request moduleapi.ActionExecutionRequestV1,
) bool {
	for _, mapping := range config.Actions {
		if mapping.PublicActionID == request.PublicActionID {
			return mapping.ProviderActionID == request.ProviderActionID &&
				request.MaxResultBytes <= mapping.MaxResultBytes
		}
	}
	return false
}

func containsPreparedActionProviderV1(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func actionContentRecordDigestV1(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(actionContentRecordDigestDomainV1))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}

// ActionExecutor is the private Host Adapter capability consumed only by the
// Core-owned Action Gateway after a persisted DispatchAttempt grants its
// one-shot permit. It is intentionally absent from moduleapi.ActionProviderV1
// and is never exposed to a model, Admission materializer, Skill, or MCP
// client.
type ActionExecutor interface {
	ExecutePrepared(
		context.Context,
		PreparedActionExecutionV1,
	) (moduleapi.ActionExecutionResultV1, error)
}
