package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var errRemoteActionCredentialUnavailable = errors.New(
	"REMOTE Action credential is unavailable",
)

type remoteActionRuntimeFlagValues struct {
	enabled        *bool
	secretMappings repeatedStringFlag
}

func bindRemoteActionRuntimeFlags(
	flags *flag.FlagSet,
) *remoteActionRuntimeFlagValues {
	values := &remoteActionRuntimeFlagValues{
		enabled: flags.Bool(
			"enable-remote-actions",
			false,
			"explicitly enable Catalog-selected REMOTE Action adapters",
		),
	}
	flags.Var(
		&values.secretMappings,
		"remote-action-secret-env",
		"exact <secret-ref>=<ENV_VAR> runtime mapping (repeatable; contains no secret)",
	)
	return values
}

func (values *remoteActionRuntimeFlagValues) config(
	_ *flag.FlagSet,
) (*productionRemoteActionRuntimeConfig, error) {
	if values == nil || values.enabled == nil {
		return nil, errors.New("REMOTE Action runtime flags are not initialized")
	}
	if !*values.enabled {
		if len(values.secretMappings) != 0 {
			return nil, errors.New(
				"--remote-action-secret-env requires explicit --enable-remote-actions",
			)
		}
		return nil, nil
	}
	if len(values.secretMappings) == 0 {
		return nil, errors.New(
			"--enable-remote-actions requires at least one --remote-action-secret-env mapping",
		)
	}

	mappings := make(map[string]string, len(values.secretMappings))
	for _, mapping := range values.secretMappings {
		separator := strings.LastIndexByte(mapping, '=')
		if separator <= 0 {
			return nil, errors.New(
				"--remote-action-secret-env must use non-empty <secret-ref>=<ENV_VAR>",
			)
		}
		secretRef := mapping[:separator]
		environment := mapping[separator+1:]
		if !validRemoteActionSecretRef(secretRef) {
			return nil, errors.New(
				"--remote-action-secret-env secret-ref must be a canonical non-empty reference",
			)
		}
		if !validEnvironmentName(environment) {
			return nil, errors.New(
				"--remote-action-secret-env must name a portable environment variable",
			)
		}
		if _, duplicate := mappings[secretRef]; duplicate {
			return nil, errors.New(
				"--remote-action-secret-env contains a duplicate secret-ref",
			)
		}
		mappings[secretRef] = environment
	}
	return &productionRemoteActionRuntimeConfig{
		SecretResolver: &environmentRemoteActionSecretResolver{
			environmentByReference: mappings,
			lookup:                 os.LookupEnv,
		},
	}, nil
}

func validRemoteActionSecretRef(value string) bool {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func remoteActionSecretResolverAvailable(
	resolver remoteactionhttp.SecretResolver,
) bool {
	if resolver == nil {
		return false
	}
	reflected := reflect.ValueOf(resolver)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return !reflected.IsNil()
	default:
		return true
	}
}

// environmentRemoteActionSecretResolver retains only an immutable
// SecretRef-to-environment-name map. It performs the environment lookup on
// every dispatch, so rotation is observed without rebuilding the Registry.
type environmentRemoteActionSecretResolver struct {
	environmentByReference map[string]string
	lookup                 func(string) (string, bool)
}

func (resolver *environmentRemoteActionSecretResolver) ResolveSecret(
	ctx context.Context,
	identity remoteactionhttp.SecretIdentityV1,
) ([]byte, error) {
	if ctx == nil || resolver == nil || resolver.lookup == nil {
		return nil, errRemoteActionCredentialUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	environment, found := resolver.environmentByReference[identity.SecretRef]
	if !found || !validEnvironmentName(environment) {
		return nil, errRemoteActionCredentialUnavailable
	}
	material, found := resolver.lookup(environment)
	if !found || material == "" {
		return nil, errRemoteActionCredentialUnavailable
	}
	return append([]byte(nil), material...), nil
}
