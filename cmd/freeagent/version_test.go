package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const injectedCommit = "0123456789abcdef0123456789abcdef01234567"

func TestRunVersionDefaultBuild(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"--version"},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("run --version: %v", err)
	}
	want := fmt.Sprintf(
		"freeagent version=dev commit=unknown target=%s/%s\n",
		runtime.GOOS,
		runtime.GOARCH,
	)
	if stdout.String() != want {
		t.Fatalf("--version output = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("--version stderr = %q, want empty", stderr.String())
	}
}

func TestFormatBuildInfoInjected(t *testing.T) {
	t.Parallel()

	got, err := formatBuildInfo(buildInfo{
		Version: "v0.1.0-dev.1",
		Commit:  injectedCommit,
		Target:  "linux/arm64",
	})
	if err != nil {
		t.Fatalf("format injected build info: %v", err)
	}
	want := "freeagent version=v0.1.0-dev.1 commit=" + injectedCommit +
		" target=linux/arm64"
	if got != want {
		t.Fatalf("injected build info = %q, want %q", got, want)
	}
}

func TestFormatBuildInfoRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	valid := buildInfo{
		Version: "v0.1.0-dev.1",
		Commit:  injectedCommit,
		Target:  "linux/amd64",
	}
	tests := []struct {
		name   string
		mutate func(*buildInfo)
		field  string
	}{
		{
			name: "empty version",
			mutate: func(info *buildInfo) {
				info.Version = ""
			},
			field: "version",
		},
		{
			name: "version without v prefix",
			mutate: func(info *buildInfo) {
				info.Version = "0.1.0"
			},
			field: "version",
		},
		{
			name: "version with leading zero",
			mutate: func(info *buildInfo) {
				info.Version = "v01.1.0"
			},
			field: "version",
		},
		{
			name: "version with numeric prerelease leading zero",
			mutate: func(info *buildInfo) {
				info.Version = "v0.1.0-01"
			},
			field: "version",
		},
		{
			name: "short commit",
			mutate: func(info *buildInfo) {
				info.Commit = injectedCommit[:39]
			},
			field: "commit",
		},
		{
			name: "uppercase commit",
			mutate: func(info *buildInfo) {
				info.Commit = strings.ToUpper(injectedCommit)
			},
			field: "commit",
		},
		{
			name: "partial release metadata",
			mutate: func(info *buildInfo) {
				info.Commit = defaultBuildCommit
			},
			field: "commit",
		},
		{
			name: "development build with injected commit",
			mutate: func(info *buildInfo) {
				info.Version = defaultBuildVersion
			},
			field: "commit",
		},
		{
			name: "target without slash",
			mutate: func(info *buildInfo) {
				info.Target = "linux-amd64"
			},
			field: "target",
		},
		{
			name: "uppercase target",
			mutate: func(info *buildInfo) {
				info.Target = "Linux/amd64"
			},
			field: "target",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			info := valid
			test.mutate(&info)
			if _, err := formatBuildInfo(info); err == nil {
				t.Fatal("formatBuildInfo succeeded, want error")
			} else if !strings.Contains(err.Error(), test.field) {
				t.Fatalf("error = %q, want field %q", err, test.field)
			}
		})
	}
}

func TestRunVersionRejectsArgumentsAndInvalidMetadataWithoutOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		info buildInfo
	}{
		{
			name: "extra argument",
			args: []string{"unexpected"},
			info: currentBuildInfo(),
		},
		{
			name: "invalid injected metadata",
			info: buildInfo{
				Version: "v0.1.0",
				Commit:  "not-a-commit",
				Target:  "linux/amd64",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			if err := runVersion(test.args, &stdout, test.info); err == nil {
				t.Fatal("runVersion succeeded, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("runVersion wrote %q before failing", stdout.String())
			}
		})
	}
}

func TestCommandUsageIncludesVersion(t *testing.T) {
	t.Parallel()

	if usage := commandUsageError().Error(); !strings.Contains(usage, "freeagent --version") {
		t.Fatalf("usage does not advertise --version: %q", usage)
	}
}

func TestVersionFileIsControlledReleaseVersion(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got, want := string(contents), "v0.1.1\n"; got != want {
		t.Fatalf("VERSION = %q, want %q", got, want)
	}
	if !semanticVersionPattern.MatchString(strings.TrimSpace(string(contents))) {
		t.Fatalf("VERSION is not a valid injectable version: %q", contents)
	}
}
