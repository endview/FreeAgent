package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/endview/freeagent/internal/deepseekmodel"
)

const defaultDeepSeekAPIKeyEnvironment = "FREEAGENT_DEEPSEEK_API_KEY"

var errDeepSeekCredentialUnavailable = errors.New(
	"DeepSeek API credential is unavailable",
)

type deepSeekRuntimeFlagValues struct {
	enabled               *bool
	credentialEnvironment *string
}

func bindDeepSeekRuntimeFlags(flags *flag.FlagSet) deepSeekRuntimeFlagValues {
	return deepSeekRuntimeFlagValues{
		enabled: flags.Bool(
			"enable-deepseek",
			false,
			"explicitly enable the exact trusted DeepSeek model adapter",
		),
		credentialEnvironment: flags.String(
			"deepseek-api-key-env",
			defaultDeepSeekAPIKeyEnvironment,
			"environment variable name holding the runtime DeepSeek credential",
		),
	}
}

func (values deepSeekRuntimeFlagValues) config(
	flags *flag.FlagSet,
) (*productionDeepSeekRuntimeConfig, error) {
	if values.enabled == nil || values.credentialEnvironment == nil {
		return nil, errors.New("DeepSeek runtime flags are not initialized")
	}
	keyEnvironmentWasSet := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "deepseek-api-key-env" {
			keyEnvironmentWasSet = true
		}
	})
	if !*values.enabled {
		if keyEnvironmentWasSet {
			return nil, errors.New(
				"--deepseek-api-key-env requires explicit --enable-deepseek",
			)
		}
		return nil, nil
	}
	if !validEnvironmentName(*values.credentialEnvironment) {
		return nil, errors.New(
			"--deepseek-api-key-env must be a portable environment variable name",
		)
	}
	return &productionDeepSeekRuntimeConfig{
		APIKeyResolver: &environmentDeepSeekAPIKeyResolver{
			name:   *values.credentialEnvironment,
			lookup: os.LookupEnv,
		},
	}, nil
}

// environmentDeepSeekAPIKeyResolver deliberately stores only an environment
// variable name. It resolves at dispatch time, returns a new owned byte slice
// on every call, and never includes credential material in diagnostics.
type environmentDeepSeekAPIKeyResolver struct {
	name      string
	reference string
	lookup    func(string) (string, bool)
}

func (resolver *environmentDeepSeekAPIKeyResolver) ResolveAPIKey(
	ctx context.Context,
	identity deepseekmodel.APIKeyIdentity,
) ([]byte, error) {
	if ctx == nil || resolver == nil || resolver.lookup == nil ||
		!validEnvironmentName(resolver.name) {
		return nil, errDeepSeekCredentialUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expectedRef := resolver.reference
	if expectedRef == "" {
		expectedRef = "env:" + resolver.name
	}
	// Empty is retained only for pre-E4 bootstrap bindings. Every
	// model-authority-ceiling/v1 invocation must match this exact local ref.
	if identity.SecretRef != "" && identity.SecretRef != expectedRef {
		return nil, errDeepSeekCredentialUnavailable
	}
	value, found := resolver.lookup(resolver.name)
	if !found || value == "" {
		return nil, errDeepSeekCredentialUnavailable
	}
	return append([]byte(nil), value...), nil
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for index, character := range []byte(value) {
		if index == 0 {
			if character != '_' &&
				(character < 'A' || character > 'Z') &&
				(character < 'a' || character > 'z') {
				return false
			}
			continue
		}
		if character != '_' &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}
