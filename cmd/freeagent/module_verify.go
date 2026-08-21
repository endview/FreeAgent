package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/internal/moduleconformance"
)

type moduleVerifyFunc func(
	context.Context,
	string,
) (moduleconformance.Report, error)

type moduleVerifySourceFunc func(
	context.Context,
	moduleconformance.SourceCandidateInput,
) (moduleconformance.Report, error)

type moduleVerifyCanonicalReadFunc func(
	context.Context,
	string,
) ([]byte, error)

type moduleVerifyDependencies struct {
	verifyLegacy  moduleVerifyFunc
	verifySource  moduleVerifySourceFunc
	readCanonical moduleVerifyCanonicalReadFunc
}

func runModuleVerify(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModuleVerifyWithDependencies(
		ctx,
		args,
		stdout,
		stderr,
		moduleVerifyDependencies{
			verifyLegacy:  moduleconformance.VerifyDirectory,
			verifySource:  moduleconformance.VerifySourceCandidateDirectory,
			readCanonical: readModuleVerifyCanonicalFile,
		},
	)
}

func runModuleVerifyWithDependency(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	verify moduleVerifyFunc,
) error {
	return runModuleVerifyWithDependencies(
		ctx,
		args,
		stdout,
		stderr,
		moduleVerifyDependencies{
			verifyLegacy:  verify,
			verifySource:  moduleconformance.VerifySourceCandidateDirectory,
			readCanonical: readModuleVerifyCanonicalFile,
		},
	)
}

func runModuleVerifyWithDependencies(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies moduleVerifyDependencies,
) error {
	if ctx == nil {
		return errors.New("freeagent module-verify: context is nil")
	}
	if dependencies.verifyLegacy == nil || dependencies.verifySource == nil ||
		dependencies.readCanonical == nil {
		return errors.New("freeagent module-verify: verify dependency is nil")
	}
	// Flag parser diagnostics may echo an untrusted flag name. Keep the public
	// boundary fixed; successful output remains stdout-only.
	flags := newFlagSet("module-verify", io.Discard)
	artifact := flags.String(
		"artifact",
		"",
		"unpacked module package directory",
	)
	sourceFlags := bindModuleVerifySourceFlags(flags)
	if err := flags.Parse(args); err != nil {
		return errors.New("freeagent module-verify: invalid flags")
	}
	if flags.NArg() != 0 || strings.TrimSpace(*artifact) == "" {
		return errors.New("freeagent module-verify: --artifact is required")
	}

	var (
		report moduleconformance.Report
		err    error
	)
	if !moduleVerifySourceMode(flags) {
		report, err = dependencies.verifyLegacy(ctx, *artifact)
	} else {
		input, failure := prepareModuleVerifySourceInput(
			ctx,
			*artifact,
			sourceFlags,
			dependencies.readCanonical,
		)
		if failure != "" {
			return moduleVerifyFailure(failure)
		}
		report, err = dependencies.verifySource(ctx, input)
	}
	if err != nil {
		return moduleVerifyFailure(moduleconformance.FailureCodeOf(err))
	}
	if err := writeCommandJSON(stdout, report); err != nil {
		return errors.New("freeagent module-verify: write report failed")
	}
	return nil
}

func moduleVerifyFailure(code moduleconformance.FailureCode) error {
	return fmt.Errorf(
		"freeagent module-verify: verification failed (%s)",
		code,
	)
}
