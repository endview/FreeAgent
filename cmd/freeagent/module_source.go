package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulesource"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleSourceRegisterResultSchemaV1  = "freeagent.module-source-register-result/v1"
	moduleSourceRefreshResultSchemaV1   = "freeagent.module-source-refresh-result/v1"
	modulePublisherRevokeResultSchemaV1 = "freeagent.module-publisher-key-revoke-result/v1"
)

type moduleSourceCommandFailureCodeV1 string

const (
	moduleSourceFailureDisabledV1       moduleSourceCommandFailureCodeV1 = "DISCOVERY_DISABLED"
	moduleSourceFailureInvalidFlagsV1   moduleSourceCommandFailureCodeV1 = "INVALID_FLAGS"
	moduleSourceFailurePolicyInvalidV1  moduleSourceCommandFailureCodeV1 = "POLICY_INVALID"
	moduleSourceFailureKeyInvalidV1     moduleSourceCommandFailureCodeV1 = "PUBLISHER_KEY_INVALID"
	moduleSourceFailureStoreBusyV1      moduleSourceCommandFailureCodeV1 = "STORE_BUSY"
	moduleSourceFailureStoreInvalidV1   moduleSourceCommandFailureCodeV1 = "STORE_INVALID"
	moduleSourceFailureSourceMissingV1  moduleSourceCommandFailureCodeV1 = "SOURCE_NOT_FOUND"
	moduleSourceFailureKeyMissingV1     moduleSourceCommandFailureCodeV1 = "PUBLISHER_KEY_NOT_FOUND"
	moduleSourceFailureKeyRevokedV1     moduleSourceCommandFailureCodeV1 = "PUBLISHER_KEY_REVOKED"
	moduleSourceFailureSourceConflictV1 moduleSourceCommandFailureCodeV1 = "SOURCE_CONFLICT"
	moduleSourceFailureSourceStaleV1    moduleSourceCommandFailureCodeV1 = "SOURCE_STALE"
	moduleSourceFailureCancelledV1      moduleSourceCommandFailureCodeV1 = "CANCELLED"
	moduleSourceFailureOutputV1         moduleSourceCommandFailureCodeV1 = "OUTPUT_FAILED"
	moduleSourceFailureInternalV1       moduleSourceCommandFailureCodeV1 = "INTERNAL_ERROR"
)

type moduleSourceRegisterStoreV1 interface {
	RegisterModuleSource(context.Context, currentstore.RegisterModuleSourceInput) (currentstore.ModuleSource, error)
	Close() error
}

type moduleSourceRefreshStoreV1 interface {
	ReadModuleSourceRefreshBasis(context.Context, string) (currentstore.ModuleSourceRefreshBasis, error)
	CommitModuleSourceRefresh(context.Context, currentstore.ModuleSourceRefreshBasis, []byte) (currentstore.ModuleDiscoverySnapshot, error)
	Close() error
}

type modulePublisherKeyStoreV1 interface {
	RevokeModulePublisherKey(context.Context, string, uint64) (currentstore.ModulePublisherKey, error)
	Close() error
}

type moduleSourceObserverV1 interface {
	Observe(context.Context, modulesource.ObserveRequest) (modulesource.Observation, error)
}

type moduleSourceRegisterDependenciesV1 struct {
	readCanonical func(context.Context, string) ([]byte, error)
	openStore     func(context.Context, string) (moduleSourceRegisterStoreV1, error)
}

type moduleSourceRefreshDependenciesV1 struct {
	openStore   func(context.Context, string) (moduleSourceRefreshStoreV1, error)
	newProvider func(modulesource.Config) (moduleSourceObserverV1, error)
}

type modulePublisherKeyRevokeDependenciesV1 struct {
	openStore func(context.Context, string) (modulePublisherKeyStoreV1, error)
}

type moduleSourceRegisterResultV1 struct {
	SchemaVersion  string `json:"schema_version"`
	Status         string `json:"status"`
	SourceID       string `json:"source_id"`
	SourcePolicyID string `json:"source_policy_id"`
	PolicyRevision uint64 `json:"policy_revision"`
	Signed         bool   `json:"signed"`
}

type moduleSourceRefreshResultV1 struct {
	SchemaVersion       string `json:"schema_version"`
	Status              string `json:"status"`
	SourceID            string `json:"source_id"`
	SourcePolicyID      string `json:"source_policy_id"`
	PolicyRevision      uint64 `json:"policy_revision"`
	IndexID             string `json:"index_id"`
	SnapshotID          string `json:"snapshot_id"`
	ObservationRevision uint64 `json:"observation_revision"`
	EntryCount          uint32 `json:"entry_count"`
	ObservedAt          string `json:"observed_at"`
}

type modulePublisherKeyRevokeResultV1 struct {
	SchemaVersion  string `json:"schema_version"`
	Status         string `json:"status"`
	PublisherKeyID string `json:"publisher_key_id"`
	KeyRevision    uint64 `json:"key_revision"`
	RevokedAt      string `json:"revoked_at"`
}

func productionModuleSourceRegisterDependenciesV1() moduleSourceRegisterDependenciesV1 {
	return moduleSourceRegisterDependenciesV1{
		readCanonical: readModuleVerifyCanonicalFile,
		openStore: func(ctx context.Context, path string) (moduleSourceRegisterStoreV1, error) {
			return currentstore.OpenExistingCurrentStore(ctx, path)
		},
	}
}

func productionModuleSourceRefreshDependenciesV1() moduleSourceRefreshDependenciesV1 {
	return moduleSourceRefreshDependenciesV1{
		openStore: func(ctx context.Context, path string) (moduleSourceRefreshStoreV1, error) {
			return currentstore.OpenExistingCurrentStore(ctx, path)
		},
		newProvider: func(config modulesource.Config) (moduleSourceObserverV1, error) {
			return modulesource.New(config)
		},
	}
}

func productionModulePublisherKeyRevokeDependenciesV1() modulePublisherKeyRevokeDependenciesV1 {
	return modulePublisherKeyRevokeDependenciesV1{
		openStore: func(ctx context.Context, path string) (modulePublisherKeyStoreV1, error) {
			return currentstore.OpenExistingCurrentStore(ctx, path)
		},
	}
}

func runModuleSourceRegister(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModuleSourceRegisterWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModuleSourceRegisterDependenciesV1(),
	)
}

func runModuleSourceRegisterWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleSourceRegisterDependenciesV1,
) (returnErr error) {
	flags := newFlagSet("module-source-register", io.Discard)
	enabled := flags.Bool("enable-module-discovery", false, "enable this explicit discovery operation")
	databasePath := flags.String("db", "", "existing Current Store database path")
	policyPath := flags.String("policy", "", "exact canonical Source Policy file")
	policyID := flags.String("policy-id", "", "expected exact Source Policy content ID")
	publisherKeyPath := flags.String("publisher-key", "", "optional exact canonical Publisher Key file")
	publisherKeyID := flags.String("publisher-key-id", "", "optional expected Publisher Key content ID")
	expectedRevision := flags.Uint64("expected-policy-revision", 0, "expected current Source Policy revision; zero creates")
	if err := flags.Parse(args); err != nil {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureDisabledV1)
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.readCanonical == nil ||
		dependencies.openStore == nil || !exactNonEmptyFlag(*databasePath) ||
		!exactNonEmptyFlag(*policyPath) || !moduleapi.ValidSHA256(*policyID) ||
		!flagWasExplicitlySetV1(flags, "expected-policy-revision") {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureInvalidFlagsV1)
	}
	keyPathPresent := *publisherKeyPath != ""
	keyIDPresent := *publisherKeyID != ""
	if keyPathPresent != keyIDPresent ||
		(keyPathPresent && (!exactNonEmptyFlag(*publisherKeyPath) || !moduleapi.ValidSHA256(*publisherKeyID))) {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureInvalidFlagsV1)
	}

	policyCanonical, err := dependencies.readCanonical(ctx, *policyPath)
	if err != nil {
		return moduleSourceCommandFailureFromErrorV1("module-source-register", moduleSourceFailurePolicyInvalidV1, err)
	}
	policy, ownedPolicy, derivedPolicyID, err := moduleapi.ParseModuleSourcePolicyV1(policyCanonical)
	if err != nil || derivedPolicyID != *policyID || !bytes.Equal(ownedPolicy, policyCanonical) {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailurePolicyInvalidV1)
	}
	var publisherCanonical []byte
	if keyPathPresent {
		publisherCanonical, err = dependencies.readCanonical(ctx, *publisherKeyPath)
		if err != nil {
			return moduleSourceCommandFailureFromErrorV1("module-source-register", moduleSourceFailureKeyInvalidV1, err)
		}
		_, ownedKey, derivedKeyID, parseErr := moduleapi.ParseModulePublisherKeyV1(publisherCanonical)
		if parseErr != nil || derivedKeyID != *publisherKeyID ||
			!bytes.Equal(ownedKey, publisherCanonical) ||
			!policy.SignatureRequired || policy.PublisherKeyID != derivedKeyID {
			return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureKeyInvalidV1)
		}
	}

	store, err := dependencies.openStore(ctx, *databasePath)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-source-register", err)
	}
	if store == nil {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureInternalV1)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && returnErr == nil {
			returnErr = moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureStoreInvalidV1)
		}
	}()
	result, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{
		PolicyCanonical:        ownedPolicy,
		PublisherKeyCanonical:  publisherCanonical,
		ExpectedPolicyRevision: *expectedRevision,
	})
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-source-register", err)
	}
	if result.SourceID != policy.SourceID || result.PolicyID != derivedPolicyID ||
		result.PolicyRevision == 0 {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureStoreInvalidV1)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, moduleSourceRegisterResultV1{
		SchemaVersion:  moduleSourceRegisterResultSchemaV1,
		Status:         "RECORDED",
		SourceID:       result.SourceID,
		SourcePolicyID: result.PolicyID,
		PolicyRevision: result.PolicyRevision,
		Signed:         result.Policy.SignatureRequired,
	}); err != nil {
		return moduleSourceCommandFailureV1("module-source-register", moduleSourceFailureOutputV1)
	}
	return nil
}

func runModuleSourceRefresh(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModuleSourceRefreshWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModuleSourceRefreshDependenciesV1(),
	)
}

func runModuleSourceRefreshWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleSourceRefreshDependenciesV1,
) (returnErr error) {
	flags := newFlagSet("module-source-refresh", io.Discard)
	enabled := flags.Bool("enable-module-discovery", false, "enable this explicit discovery operation")
	httpsEnabled := flags.Bool("enable-https-module-discovery", false, "enable exact-allowlisted HTTPS index observation")
	databasePath := flags.String("db", "", "existing Current Store database path")
	sourceID := flags.String("source-id", "", "registered module source identity")
	localDirectory := flags.String("local-directory", "", "exact local source root containing index.json")
	httpsIndexURL := flags.String("https-index-url", "", "exact HTTPS discovery index URL")
	var httpsOrigins repeatedStringFlag
	flags.Var(&httpsOrigins, "allow-https-source-origin", "exact HTTPS origin allowed for this operation; repeatable")
	if err := flags.Parse(args); err != nil {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureDisabledV1)
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.openStore == nil ||
		dependencies.newProvider == nil || !exactNonEmptyFlag(*databasePath) ||
		!exactNonEmptyFlag(*sourceID) {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInvalidFlagsV1)
	}
	localSelected := *localDirectory != ""
	httpsSelected := *httpsIndexURL != ""
	localFlagSet := flagWasExplicitlySetV1(flags, "local-directory")
	httpsFlagSet := flagWasExplicitlySetV1(flags, "https-index-url")
	if localFlagSet == httpsFlagSet || localSelected != localFlagSet ||
		httpsSelected != httpsFlagSet || localSelected == httpsSelected ||
		(localSelected && (!exactNonEmptyFlag(*localDirectory) || *httpsEnabled || len(httpsOrigins) != 0 ||
			flagWasExplicitlySetV1(flags, "enable-https-module-discovery") ||
			flagWasExplicitlySetV1(flags, "allow-https-source-origin") || httpsFlagSet)) ||
		(httpsSelected && (!exactNonEmptyFlag(*httpsIndexURL) || !*httpsEnabled || len(httpsOrigins) == 0)) {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInvalidFlagsV1)
	}

	provider, err := dependencies.newProvider(modulesource.Config{
		HTTPSIndexEnabled:    httpsSelected,
		HTTPSOriginAllowlist: append([]string(nil), httpsOrigins...),
	})
	if err != nil {
		return moduleSourceCommandFailureFromProviderErrorV1("module-source-refresh", err)
	}
	if provider == nil {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInternalV1)
	}
	store, err := dependencies.openStore(ctx, *databasePath)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-source-refresh", err)
	}
	if store == nil {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInternalV1)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && returnErr == nil {
			returnErr = moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureStoreInvalidV1)
		}
	}()
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, *sourceID)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-source-refresh", err)
	}
	if basis.Source.SourceID != *sourceID || basis.Source.PolicyID == "" ||
		basis.Source.PolicyRevision == 0 {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureStoreInvalidV1)
	}
	observation, err := provider.Observe(ctx, modulesource.ObserveRequest{
		SourcePolicyCanonical: basis.Source.PolicyCanonical,
		SourcePolicyID:        basis.Source.PolicyID,
		LocalDirectory:        *localDirectory,
		HTTPSIndexURL:         *httpsIndexURL,
	})
	if err != nil {
		return moduleSourceCommandFailureFromProviderErrorV1("module-source-refresh", err)
	}
	if observation.SourcePolicyID != basis.Source.PolicyID ||
		!moduleapi.ValidSHA256(observation.IndexID) ||
		!moduleapi.ValidSHA256(observation.SnapshotID) {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureInternalV1)
	}
	committed, err := store.CommitModuleSourceRefresh(ctx, basis, observation.IndexCanonical)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-source-refresh", err)
	}
	if committed.SourceID != basis.Source.SourceID ||
		committed.SourcePolicyID != observation.SourcePolicyID ||
		committed.IndexID != observation.IndexID ||
		committed.SnapshotID != observation.SnapshotID ||
		!bytes.Equal(committed.IndexCanonical, observation.IndexCanonical) ||
		!bytes.Equal(committed.SnapshotCanonical, observation.SnapshotCanonical) ||
		committed.SourcePolicyRevision != basis.Source.PolicyRevision ||
		committed.ObservationRevision == 0 {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureStoreInvalidV1)
	}
	if uint64(len(committed.Snapshot.Entries)) > uint64(^uint32(0)) {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureStoreInvalidV1)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, moduleSourceRefreshResultV1{
		SchemaVersion:       moduleSourceRefreshResultSchemaV1,
		Status:              "OBSERVED",
		SourceID:            committed.SourceID,
		SourcePolicyID:      committed.SourcePolicyID,
		PolicyRevision:      committed.SourcePolicyRevision,
		IndexID:             committed.IndexID,
		SnapshotID:          committed.SnapshotID,
		ObservationRevision: committed.ObservationRevision,
		EntryCount:          uint32(len(committed.Snapshot.Entries)),
		ObservedAt:          committed.ObservedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return moduleSourceCommandFailureV1("module-source-refresh", moduleSourceFailureOutputV1)
	}
	return nil
}

func runModulePublisherKeyRevoke(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModulePublisherKeyRevokeWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModulePublisherKeyRevokeDependenciesV1(),
	)
}

func runModulePublisherKeyRevokeWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies modulePublisherKeyRevokeDependenciesV1,
) (returnErr error) {
	flags := newFlagSet("module-publisher-key-revoke", io.Discard)
	enabled := flags.Bool("enable-module-discovery", false, "enable this explicit discovery operation")
	databasePath := flags.String("db", "", "existing Current Store database path")
	keyID := flags.String("key-id", "", "exact Publisher Key identity")
	expectedRevision := flags.Uint64("expected-key-revision", 0, "expected live Publisher Key revision")
	if err := flags.Parse(args); err != nil {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureDisabledV1)
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.openStore == nil ||
		!exactNonEmptyFlag(*databasePath) || !moduleapi.ValidSHA256(*keyID) ||
		*expectedRevision == 0 {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureInvalidFlagsV1)
	}
	store, err := dependencies.openStore(ctx, *databasePath)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-publisher-key-revoke", err)
	}
	if store == nil {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureInternalV1)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && returnErr == nil {
			returnErr = moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureStoreInvalidV1)
		}
	}()
	result, err := store.RevokeModulePublisherKey(ctx, *keyID, *expectedRevision)
	if err != nil {
		return moduleSourceCommandFailureFromStoreErrorV1("module-publisher-key-revoke", err)
	}
	if result.PublisherKeyID != *keyID || result.Revision == 0 || result.RevokedAt == nil {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureStoreInvalidV1)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, modulePublisherKeyRevokeResultV1{
		SchemaVersion:  modulePublisherRevokeResultSchemaV1,
		Status:         "REVOKED",
		PublisherKeyID: result.PublisherKeyID,
		KeyRevision:    result.Revision,
		RevokedAt:      result.RevokedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return moduleSourceCommandFailureV1("module-publisher-key-revoke", moduleSourceFailureOutputV1)
	}
	return nil
}

func flagWasExplicitlySetV1(flags interface{ Visit(func(*flag.Flag)) }, name string) bool {
	found := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == name {
			found = true
		}
	})
	return found
}

func moduleSourceCommandFailureV1(command string, code moduleSourceCommandFailureCodeV1) error {
	return fmt.Errorf("freeagent %s: failed (%s)", command, code)
}

func moduleSourceCommandFailureFromErrorV1(command string, fallback moduleSourceCommandFailureCodeV1, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return moduleSourceCommandFailureV1(command, moduleSourceFailureCancelledV1)
	}
	return moduleSourceCommandFailureV1(command, fallback)
}

func moduleSourceCommandFailureFromStoreErrorV1(command string, err error) error {
	code := moduleSourceFailureStoreInvalidV1
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = moduleSourceFailureCancelledV1
	case errors.Is(err, currentstore.ErrOwnerActive):
		code = moduleSourceFailureStoreBusyV1
	case errors.Is(err, currentstore.ErrModuleSourceNotFound):
		code = moduleSourceFailureSourceMissingV1
	case errors.Is(err, currentstore.ErrModulePublisherKeyNotFound):
		code = moduleSourceFailureKeyMissingV1
	case errors.Is(err, currentstore.ErrModulePublisherKeyRevoked):
		code = moduleSourceFailureKeyRevokedV1
	case errors.Is(err, currentstore.ErrModuleSourceConflict):
		code = moduleSourceFailureSourceConflictV1
	case errors.Is(err, currentstore.ErrModuleSourceStale):
		code = moduleSourceFailureSourceStaleV1
	}
	return moduleSourceCommandFailureV1(command, code)
}

func moduleSourceCommandFailureFromProviderErrorV1(command string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return moduleSourceCommandFailureV1(command, moduleSourceFailureCancelledV1)
	}
	code := modulesource.FailureCodeOf(err)
	if code == "" {
		return moduleSourceCommandFailureV1(command, moduleSourceFailureInternalV1)
	}
	return fmt.Errorf("freeagent %s: failed (%s)", command, code)
}
