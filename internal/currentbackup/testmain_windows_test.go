//go:build windows

package currentbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/moduleartifactstore"
)

const (
	currentBackupWindowsTestTempRootEnvV1 = "FREEAGENT_CURRENTBACKUP_WINDOWS_TEST_TEMP_ROOT_V1"
	currentBackupWindowsTestTempPrefixV1  = ".b-"
)

// TestMain places all Go 1.26 testing.TempDir trees below a private, protected root.
// Production artifact-root validation intentionally rejects the ordinary
// shared Windows temp hierarchy because another local principal may have
// namespace-takeover rights there.
func TestMain(m *testing.M) {
	os.Exit(runWithPrivateWindowsTestTemp(m))
}

func runWithPrivateWindowsTestTemp(m *testing.M) int {
	rootPath, inherited, err := inheritedPrivateWindowsTestTempV1(
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate inherited private Windows test temp: %v\n", err)
		return 2
	}
	if inherited {
		return m.Run()
	}
	outerGoTmpDir, outerGoTmpDirPresent := os.LookupEnv("GOTMPDIR")
	currentBackupWindowsTestOuterGoTmpDirV1 = currentBackupWindowsTestOuterGoTmpDirStateV1{
		owner:   true,
		present: outerGoTmpDirPresent,
		value:   outerGoTmpDir,
	}
	home, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve private test cache: %v\n", err)
		return 2
	}
	name := privateWindowsTestTempNameV1(
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid(),
		time.Now().UnixNano(),
	)
	rootPath = filepath.Join(home, name)
	if _, err := moduleartifactstore.ProvisionArtifactRootV1(home, name); err != nil {
		fmt.Fprintf(os.Stderr, "provision private Windows test temp: %v\n", err)
		return 2
	}
	if err := commitPrivateWindowsTestTempEnvironmentV1(
		currentBackupWindowsTestTempRootEnvV1,
		rootPath,
		os.Setenv,
	); err != nil {
		fmt.Fprintf(os.Stderr, "set private Windows test temp: %v\n", err)
		_ = os.RemoveAll(rootPath)
		return 2
	}
	code := m.Run()
	if err := os.RemoveAll(rootPath); err != nil {
		fmt.Fprintf(os.Stderr, "remove private Windows test temp: %v\n", err)
		if code == 0 {
			code = 2
		}
	}
	return code
}

func inheritedPrivateWindowsTestTempV1(markerEnv, prefix string) (string, bool, error) {
	return inheritedPrivateWindowsTestTempForOwnerPIDV1(
		markerEnv,
		prefix,
		os.Getppid(),
	)
}

func inheritedPrivateWindowsTestTempForOwnerPIDV1(
	markerEnv string,
	prefix string,
	expectedOwnerPID int,
) (string, bool, error) {
	marker, present := os.LookupEnv(markerEnv)
	if !present {
		return "", false, nil
	}
	if marker == "" {
		return "", true, fmt.Errorf("%s is present but empty", markerEnv)
	}
	if err := validateInheritedPrivateWindowsTestTempV1(
		marker,
		markerEnv,
		prefix,
		expectedOwnerPID,
	); err != nil {
		return "", true, err
	}
	return marker, true, nil
}

func validateInheritedPrivateWindowsTestTempV1(
	marker string,
	markerEnv string,
	prefix string,
	expectedOwnerPID int,
) error {
	for _, key := range []string{markerEnv, "GOTMPDIR", "TEMP", "TMP"} {
		value, present := os.LookupEnv(key)
		if !present {
			return fmt.Errorf("%s is missing from the inherited environment", key)
		}
		if value == "" || value != marker {
			return fmt.Errorf("%s does not equal the inherited root marker", key)
		}
	}
	home, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("resolve private test cache: %w", err)
	}
	cleanHome := filepath.Clean(home)
	if !filepath.IsAbs(home) || !strings.EqualFold(home, cleanHome) {
		return fmt.Errorf("user cache directory is not a clean absolute path")
	}
	if !strings.EqualFold(filepath.Clean(filepath.Dir(marker)), cleanHome) {
		return fmt.Errorf("inherited root parent is not the user cache directory")
	}
	name := filepath.Base(marker)
	if !validPrivateWindowsTestTempNameV1(name, prefix) {
		return fmt.Errorf("inherited root name is invalid")
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
	ownerPID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || expectedOwnerPID <= 0 || ownerPID != uint64(expectedOwnerPID) {
		return fmt.Errorf("inherited root owner PID does not match the parent process")
	}
	if _, err := moduleartifactstore.SelectArtifactRootV1(marker); err != nil {
		return fmt.Errorf("select inherited private Windows test temp: %w", err)
	}
	return nil
}

func commitPrivateWindowsTestTempEnvironmentV1(
	markerEnv string,
	rootPath string,
	setenv func(string, string) error,
) error {
	for _, key := range []string{"GOTMPDIR", "TEMP", "TMP", markerEnv} {
		if err := setenv(key, rootPath); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return nil
}

func privateWindowsTestTempNameV1(prefix string, pid int, unixNano int64) string {
	return prefix + strconv.Itoa(pid) + "-" + strconv.FormatInt(unixNano, 10)
}

func validPrivateWindowsTestTempNameV1(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
	return len(parts) == 2 &&
		positiveCanonicalDecimalV1(parts[0]) &&
		positiveCanonicalDecimalV1(parts[1])
}

func positiveCanonicalDecimalV1(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func TestPrivateWindowsTestTempNameV1(t *testing.T) {
	name := privateWindowsTestTempNameV1(currentBackupWindowsTestTempPrefixV1, 17, 123456789)
	if name != ".b-17-123456789" {
		t.Fatalf("private Windows test temp name = %q", name)
	}
	if !validPrivateWindowsTestTempNameV1(name, currentBackupWindowsTestTempPrefixV1) {
		t.Fatalf("generated private Windows test temp name %q is invalid", name)
	}
	for _, invalid := range []string{
		".b-0-1",
		".b-1-0",
		".b-01-1",
		".b-1-01",
		".b-+1-2",
		".b-1--2",
		".b-1-2-3",
		".c-1-2",
	} {
		if validPrivateWindowsTestTempNameV1(invalid, currentBackupWindowsTestTempPrefixV1) {
			t.Fatalf("invalid private Windows test temp name accepted: %q", invalid)
		}
	}
}

func TestInheritedPrivateWindowsTestTempIsValidatedAndReused(t *testing.T) {
	marker := os.Getenv(currentBackupWindowsTestTempRootEnvV1)
	rootPath, inherited, err := inheritedPrivateWindowsTestTempForOwnerPIDV1(
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid(),
	)
	if err != nil || !inherited || rootPath != marker || marker == "" {
		t.Fatalf(
			"inherited private Windows test temp: root=%q marker=%q inherited=%t err=%v",
			rootPath,
			marker,
			inherited,
			err,
		)
	}
	t.Setenv("TMP", marker+"-mismatch")
	if _, inherited, err := inheritedPrivateWindowsTestTempForOwnerPIDV1(
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid(),
	); err == nil || !inherited {
		t.Fatalf("mismatched inherited private Windows test temp: inherited=%t err=%v", inherited, err)
	}
}

func TestInheritedPrivateWindowsTestTempRejectsPresentEmptyAndMissingValues(t *testing.T) {
	marker, present := os.LookupEnv(currentBackupWindowsTestTempRootEnvV1)
	if !present || marker == "" {
		t.Fatal("owner marker is unexpectedly missing or empty")
	}
	t.Setenv(currentBackupWindowsTestTempRootEnvV1, "")
	if _, inherited, err := inheritedPrivateWindowsTestTempForOwnerPIDV1(
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid(),
	); err == nil || !inherited {
		t.Fatalf("present-empty marker: inherited=%t err=%v", inherited, err)
	}
	t.Setenv(currentBackupWindowsTestTempRootEnvV1, marker)

	value, present := os.LookupEnv("TEMP")
	if !present {
		t.Fatal("owner TEMP is unexpectedly missing")
	}
	if err := os.Unsetenv("TEMP"); err != nil {
		t.Fatalf("unset TEMP: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("TEMP", value); err != nil {
			t.Errorf("restore TEMP: %v", err)
		}
	})
	if _, inherited, err := inheritedPrivateWindowsTestTempForOwnerPIDV1(
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid(),
	); err == nil || !inherited {
		t.Fatalf("missing TEMP: inherited=%t err=%v", inherited, err)
	}
	if err := os.Setenv("TEMP", value); err != nil {
		t.Fatalf("restore TEMP: %v", err)
	}
}

func TestInheritedPrivateWindowsTestTempRejectsWrongParentPID(t *testing.T) {
	marker := os.Getenv(currentBackupWindowsTestTempRootEnvV1)
	if err := validateInheritedPrivateWindowsTestTempV1(
		marker,
		currentBackupWindowsTestTempRootEnvV1,
		currentBackupWindowsTestTempPrefixV1,
		os.Getpid()+1,
	); err == nil {
		t.Fatal("inherited private Windows test temp accepted the wrong parent PID")
	}
}

func TestPrivateWindowsTestTempMarkerCommitsLast(t *testing.T) {
	var keys []string
	if err := commitPrivateWindowsTestTempEnvironmentV1(
		currentBackupWindowsTestTempRootEnvV1,
		`C:\protected-test-root`,
		func(key, value string) error {
			if value != `C:\protected-test-root` {
				return fmt.Errorf("unexpected value %q", value)
			}
			keys = append(keys, key)
			return nil
		},
	); err != nil {
		t.Fatalf("commit private Windows test temp environment: %v", err)
	}
	want := "GOTMPDIR,TEMP,TMP," + currentBackupWindowsTestTempRootEnvV1
	if got := strings.Join(keys, ","); got != want {
		t.Fatalf("private Windows test temp environment order = %q, want %q", got, want)
	}
	keys = nil
	err := commitPrivateWindowsTestTempEnvironmentV1(
		currentBackupWindowsTestTempRootEnvV1,
		`C:\protected-test-root`,
		func(key, _ string) error {
			keys = append(keys, key)
			if key == "TMP" {
				return fmt.Errorf("injected TMP failure")
			}
			return nil
		},
	)
	if err == nil || strings.Join(keys, ",") != "GOTMPDIR,TEMP,TMP" {
		t.Fatalf("failed private Windows test temp commit: keys=%q err=%v", keys, err)
	}
}

func TestWindowsTestTempUsesProtectedGoTmpDir(t *testing.T) {
	tempRoot := os.Getenv("GOTMPDIR")
	if tempRoot == "" {
		t.Fatal("GOTMPDIR is empty")
	}
	for _, key := range []string{"TEMP", "TMP"} {
		if value := os.Getenv(key); value == "" || value != tempRoot {
			t.Fatalf("%s = %q, want non-empty protected root %q", key, value, tempRoot)
		}
	}
	tempDir := t.TempDir()
	relative, err := filepath.Rel(tempRoot, tempDir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		t.Fatalf("testing.TempDir %q is not below GOTMPDIR %q: relative=%q err=%v", tempDir, tempRoot, relative, err)
	}
	if _, err := moduleartifactstore.ProvisionArtifactRootV1(tempDir, "testmain-artifact-root"); err != nil {
		t.Fatalf("provision artifact root below testing.TempDir: %v", err)
	}
}
