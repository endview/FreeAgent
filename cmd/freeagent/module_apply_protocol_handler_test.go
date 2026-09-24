package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyProtocolHandlerTableV1IsExactOrderedCorePolicy(t *testing.T) {
	t.Parallel()

	want := [9]moduleApplyLocalPolicyV1{
		{
			Port:                                  productionModelPort,
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ModelBindingConfigSchemaV2,
			ModuleID:                              localDeepSeekModuleID,
			ExactVersion:                          localDeepSeekVersion,
			ArtifactDigest:                        localDeepSeekDigest,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       deepseekmodel.AdapterIdentityV1,
			HandlerKind:                           moduleApplyHandlerDeepSeekModelV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
		{
			Port:            productionContextPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestDeclarative,
			RuntimeProtocol: moduleapi.RuntimeProtocolStaticV1,
			ConsumerSchema:  moduleapi.ContextBindingConfigSchemaV1,
			ExecutionClass:  moduleapi.ExecutionDeclarative,
			AdapterIdentity: declarativeAdapterID,
			HandlerKind:     moduleApplyHandlerDeclarativeContextV1,
		},
		{
			Port:            productionContextPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:  moduleapi.KnowledgeContextBindingSchemaV1,
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: localKnowledgeAdapterID,
			HandlerKind:     moduleApplyHandlerKnowledgeContextV1,
		},
		{
			Port:            productionContextPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:  moduleapi.MemoryContextBindingSchemaV1,
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: localMemoryAdapterID,
			HandlerKind:     moduleApplyHandlerMemoryContextV1,
		},
		{
			Port:                  productionActionPort,
			RuntimeMode:           moduleapi.RuntimeModeRequestLocalProcess,
			RuntimeProtocol:       moduleapi.RuntimeProtocolMCPStdio20251125,
			ConsumerSchema:        moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:        moduleapi.ExecutionLocalProcess,
			AdapterIdentity:       mcpstdio.AdapterIdentityV1,
			HandlerKind:           moduleApplyHandlerMCPActionV1,
			RequiresLocalMCPGrant: true,
		},
		{
			Port:                              productionActionPort,
			RuntimeMode:                       moduleapi.RuntimeModeRequestRemote,
			RuntimeProtocol:                   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			ConsumerSchema:                    moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                    moduleapi.ExecutionRemote,
			AdapterIdentity:                   remoteactionhttp.AdapterIdentityV1,
			HandlerKind:                       moduleApplyHandlerRemoteActionHTTPV1,
			RequiresRemoteActionArtifactGrant: true,
		},
		{
			Port:                            productionActionPort,
			RuntimeMode:                     moduleapi.RuntimeModeRequestWASM,
			RuntimeProtocol:                 moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			ConsumerSchema:                  moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                  moduleapi.ExecutionWASM,
			AdapterIdentity:                 wasmaction.AdapterIdentityV1,
			HandlerKind:                     moduleApplyHandlerWASMActionV1,
			RequiresWASMActionArtifactGrant: true,
		},
		{
			Port:                                  productionActionPort,
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ActionBindingConfigSchemaV1,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       localTextStatsAdapterID,
			HandlerKind:                           moduleApplyHandlerTextStatsActionV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
		{
			Port:                                  productionChannelPort,
			RuntimeMode:                           moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol:                       moduleapi.RuntimeProtocolGoInProcessV1,
			ConsumerSchema:                        moduleapi.ChannelBindingConfigSchemaV1,
			ExecutionClass:                        moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:                       loopbackchannel.AdapterIdentityV1,
			HandlerKind:                           moduleApplyHandlerLoopbackChannelV1,
			RequiresTrustedInProcessArtifactGrant: true,
		},
	}

	got := moduleApplyProtocolHandlerTableV1()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Core protocol handler table = %#v, want %#v", got, want)
	}
	if err := validateModuleApplyProtocolHandlerTableV1(got[:]); err != nil {
		t.Fatalf("validate Core protocol handler table: %v", err)
	}

	for index, handler := range want {
		resolved, err := resolveModuleApplyProtocolHandlerV1(
			handler.protocolHandlerKeyV1(),
		)
		if err != nil {
			t.Fatalf("resolve exact handler %d: %v", index, err)
		}
		if !reflect.DeepEqual(resolved, handler) {
			t.Fatalf("resolved exact handler %d = %#v, want %#v", index, resolved, handler)
		}
	}

	// Callers receive a value copy, not a mutable registry. A package Manifest
	// therefore has no object through which it could replace Core policy.
	got[1].AdapterIdentity = "manifest-selected-adapter"
	fresh := moduleApplyProtocolHandlerTableV1()
	if fresh[1].AdapterIdentity != declarativeAdapterID {
		t.Fatalf("Core protocol table retained caller mutation: %#v", fresh[1])
	}
}

func TestModuleApplyProtocolHandlerTableV1DuplicateExactKeyFailsClosed(t *testing.T) {
	t.Parallel()

	table := moduleApplyProtocolHandlerTableV1()
	duplicate := append(table[:], table[0])

	// The duplicate is unrelated to the requested Knowledge key. Resolution
	// must still reject the whole Core table instead of accepting a partial map.
	_, err := resolveModuleApplyProtocolHandlerFromTableV1(
		duplicate,
		table[1].protocolHandlerKeyV1(),
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate exact key") {
		t.Fatalf("duplicate exact key error = %v", err)
	}
}

func TestModuleApplyProtocolHandlerTableV1MalformedKnownKindFailsClosed(t *testing.T) {
	t.Parallel()

	table := moduleApplyProtocolHandlerTableV1()
	malformed := table[5]
	malformed.ExecutionClass = moduleapi.ExecutionLocalProcess
	table[5] = malformed
	if err := validateModuleApplyProtocolHandlerTableV1(table[:]); err == nil ||
		!strings.Contains(err.Error(), "invalid fixed handler combination") {
		t.Fatalf("malformed known handler error = %v", err)
	}
}

func TestModuleApplyProtocolHandlerTableV1UnknownExactKeyFailsClosed(t *testing.T) {
	t.Parallel()

	table := moduleApplyProtocolHandlerTableV1()
	key := table[0].protocolHandlerKeyV1()
	key.RuntimeProtocol = "unsupported/v1"
	if _, err := resolveModuleApplyProtocolHandlerV1(key); !errors.Is(
		err,
		errModuleApplyProtocolHandlerNotFoundV1,
	) {
		t.Fatalf("unknown exact key error = %v", err)
	}

	key = table[1].protocolHandlerKeyV1()
	key.ConsumerSchema = "unsupported-context-binding/v1"
	if _, err := resolveModuleApplyProtocolHandlerV1(key); !errors.Is(
		err,
		errModuleApplyProtocolHandlerNotFoundV1,
	) {
		t.Fatalf("unknown consumer schema error = %v", err)
	}
}
