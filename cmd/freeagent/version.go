package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
)

const (
	defaultBuildVersion = "dev"
	defaultBuildCommit  = "unknown"
)

// These variables are intentionally package-level strings so release builds can
// set them deterministically with go build -ldflags -X=main.<name>=<value>.
var (
	buildVersion = defaultBuildVersion
	buildCommit  = defaultBuildCommit
	buildTarget  = runtime.GOOS + "/" + runtime.GOARCH
)

var (
	semanticVersionPattern = regexp.MustCompile(
		`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
	)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	targetPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
)

type buildInfo struct {
	Version string
	Commit  string
	Target  string
}

func isVersionRequest(args []string) bool {
	return len(args) > 0 && args[0] == "--version"
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Target:  buildTarget,
	}
}

func runVersion(args []string, stdout io.Writer, info buildInfo) error {
	if len(args) != 0 {
		return errors.New("freeagent --version: arguments are not accepted")
	}
	if stdout == nil {
		return errors.New("freeagent --version: stdout is nil")
	}
	line, err := formatBuildInfo(info)
	if err != nil {
		return fmt.Errorf("freeagent --version: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, line); err != nil {
		return fmt.Errorf("freeagent --version: write output: %w", err)
	}
	return nil
}

func formatBuildInfo(info buildInfo) (string, error) {
	if err := validateBuildInfo(info); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"freeagent version=%s commit=%s target=%s",
		info.Version,
		info.Commit,
		info.Target,
	), nil
}

func validateBuildInfo(info buildInfo) error {
	if info.Version == defaultBuildVersion {
		if info.Commit != defaultBuildCommit {
			return errors.New("invalid development build commit: expected unknown")
		}
	} else {
		if !semanticVersionPattern.MatchString(info.Version) {
			return fmt.Errorf(
				"invalid build version %q: expected dev or v-prefixed SemVer",
				info.Version,
			)
		}
		if !commitPattern.MatchString(info.Commit) {
			return fmt.Errorf(
				"invalid build commit %q: expected 40 lowercase hexadecimal characters",
				info.Commit,
			)
		}
	}
	if !targetPattern.MatchString(info.Target) {
		return fmt.Errorf(
			"invalid build target %q: expected lowercase GOOS/GOARCH",
			info.Target,
		)
	}
	return nil
}
