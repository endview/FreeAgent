package main

import (
	"errors"
	"flag"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type wasmActionRuntimeFlagValues struct {
	enabled         *bool
	artifactDigests repeatedStringFlag
}

func bindWASMActionRuntimeFlags(
	flags *flag.FlagSet,
) *wasmActionRuntimeFlagValues {
	values := &wasmActionRuntimeFlagValues{
		enabled: flags.Bool(
			"enable-wasm-actions",
			false,
			"explicitly enable allowlisted Catalog-selected WASM Action adapters",
		),
	}
	flags.Var(
		&values.artifactDigests,
		"allow-wasm-runtime-artifact",
		"exact Catalog-selected WASM Action artifact SHA-256 allowed at runtime (repeatable)",
	)
	return values
}

func (values *wasmActionRuntimeFlagValues) config(
	_ *flag.FlagSet,
) (*productionWASMActionRuntimeConfig, error) {
	if values == nil || values.enabled == nil {
		return nil, errors.New("WASM Action runtime flags are not initialized")
	}
	if !*values.enabled {
		if len(values.artifactDigests) != 0 {
			return nil, errors.New(
				"--allow-wasm-runtime-artifact requires explicit --enable-wasm-actions",
			)
		}
		return nil, nil
	}
	if len(values.artifactDigests) == 0 {
		return nil, errors.New(
			"--enable-wasm-actions requires at least one --allow-wasm-runtime-artifact",
		)
	}

	digests := make([]string, 0, len(values.artifactDigests))
	seen := make(map[string]struct{}, len(values.artifactDigests))
	for _, digest := range values.artifactDigests {
		if !moduleapi.ValidSHA256(digest) {
			return nil, errors.New(
				"--allow-wasm-runtime-artifact must be a canonical lowercase SHA-256",
			)
		}
		if _, duplicate := seen[digest]; duplicate {
			return nil, errors.New(
				"--allow-wasm-runtime-artifact contains a duplicate digest",
			)
		}
		seen[digest] = struct{}{}
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	return &productionWASMActionRuntimeConfig{
		Enabled:                true,
		AllowedArtifactDigests: digests,
	}, nil
}
