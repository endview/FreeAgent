package main

import (
	"context"
	"errors"
	"flag"
	"os"

	"github.com/endview/freeagent/internal/zhipumodel"
)

const defaultZhipuAPIKeyEnvironment = "FREEAGENT_ZHIPU_API_KEY"

var errZhipuCredentialUnavailable = errors.New("Zhipu API credential is unavailable")

type zhipuRuntimeFlagValues struct {
	enabled               *bool
	credentialEnvironment *string
}

func bindZhipuRuntimeFlags(flags *flag.FlagSet) zhipuRuntimeFlagValues {
	return zhipuRuntimeFlagValues{
		enabled:               flags.Bool("enable-zhipu", false, "explicitly enable the exact trusted Zhipu GLM model adapter"),
		credentialEnvironment: flags.String("zhipu-api-key-env", defaultZhipuAPIKeyEnvironment, "environment variable name holding the runtime Zhipu credential"),
	}
}

func (values zhipuRuntimeFlagValues) config(flags *flag.FlagSet) (*productionZhipuRuntimeConfig, error) {
	if values.enabled == nil || values.credentialEnvironment == nil {
		return nil, errors.New("Zhipu runtime flags are not initialized")
	}
	keyEnvironmentWasSet := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "zhipu-api-key-env" {
			keyEnvironmentWasSet = true
		}
	})
	if !*values.enabled {
		if keyEnvironmentWasSet {
			return nil, errors.New("--zhipu-api-key-env requires explicit --enable-zhipu")
		}
		return nil, nil
	}
	if !validEnvironmentName(*values.credentialEnvironment) {
		return nil, errors.New("--zhipu-api-key-env must be a portable environment variable name")
	}
	return &productionZhipuRuntimeConfig{APIKeyResolver: &environmentZhipuAPIKeyResolver{name: *values.credentialEnvironment, lookup: os.LookupEnv}}, nil
}

type environmentZhipuAPIKeyResolver struct {
	name      string
	reference string
	lookup    func(string) (string, bool)
}

func (resolver *environmentZhipuAPIKeyResolver) ResolveAPIKey(ctx context.Context, identity zhipumodel.APIKeyIdentity) ([]byte, error) {
	if ctx == nil || resolver == nil || resolver.lookup == nil || !validEnvironmentName(resolver.name) {
		return nil, errZhipuCredentialUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expectedRef := resolver.reference
	if expectedRef == "" {
		expectedRef = "env:" + resolver.name
	}
	if identity.SecretRef != "" && identity.SecretRef != expectedRef {
		return nil, errZhipuCredentialUnavailable
	}
	value, found := resolver.lookup(resolver.name)
	if !found || value == "" {
		return nil, errZhipuCredentialUnavailable
	}
	return append([]byte(nil), value...), nil
}

var _ zhipumodel.APIKeyResolver = (*environmentZhipuAPIKeyResolver)(nil)
