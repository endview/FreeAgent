package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestDoctorReportsIndependentFailuresWithoutQuotingContents(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "keys.json")
	t.Setenv("FREEAGENT_KEYS_FILE", keys)
	data, _ := initCLI(t)
	const secret = "sk-doctor-private-value-never-quote"
	for _, path := range []string{filepath.Join(data, "config.json"), keys} {
		if err := os.WriteFile(path, []byte(`{"`+secret+`":"private"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(data, "postgres.json")); err != nil {
		t.Fatal(err)
	}
	raw, stderr, err := invoke(t, "", "doctor", "--data-dir", data)
	var report struct {
		OK     bool          `json:"ok"`
		Checks []doctorCheck `json:"checks"`
	}
	if exitCode(err) != 1 || json.Unmarshal([]byte(raw), &report) != nil || report.OK {
		t.Fatalf("diagnosis did not report failure as JSON: exit=%d raw=%q err=%v", exitCode(err), raw, err)
	}
	failed := map[string]bool{}
	for _, check := range report.Checks {
		if check.Status == "error" && check.Action != "" {
			failed[check.ID] = true
		}
	}
	if !failed["config"] || !failed["store"] || !failed["keys"] {
		t.Fatalf("an early error hid an independent failure: %+v", report.Checks)
	}
	if strings.Contains(raw+stderr, secret) {
		t.Fatal("diagnosis copied private content from a decoding error")
	}
	if _, err := os.Stat(filepath.Join(data, "postgres.json")); !os.IsNotExist(err) {
		t.Fatal("diagnosis recreated the missing database")
	}
}

func TestDoctorExplainsDisabledProviderAndKeepsItDisabled(t *testing.T) {
	data, workspace := initCLI(t)
	key := fixtureKey(t, "https://127.0.0.1:1", harness.KeySourceEnv, "ABSENT_DOCTOR_KEY", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	config.Providers = nil
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(data, "config.json"))
	raw, stderr, err := invoke(t, "", "doctor", "--data-dir", data, "--text")
	if err != nil || !strings.Contains(raw, "Provider is disabled") || !strings.Contains(raw, "Next:") ||
		!strings.Contains(raw, "no Provider request") {
		t.Fatalf("text diagnosis=%q stderr=%q err=%v", raw, stderr, err)
	}
	after, _ := os.ReadFile(filepath.Join(data, "config.json"))
	if string(before) != string(after) {
		t.Fatal("diagnosis modified Provider settings")
	}
}

func TestDoctorMissingDirectoryIsDiagnosedWithoutInitialization(t *testing.T) {
	data := filepath.Join(t.TempDir(), "absent")
	raw, _, err := invoke(t, "", "doctor", "--data-dir", data)
	if exitCode(err) != 1 || !strings.Contains(raw, "config_error") || !strings.Contains(raw, "store_error") {
		t.Fatalf("missing directory diagnosis=%q err=%v", raw, err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("doctor initialized the missing data directory")
	}
}

func TestDoctorAutoAllowsAnImplicitPoolWithoutAssessmentModel(t *testing.T) {
	config := harness.Config{DefaultSelection: "auto", Models: map[string]harness.ModelConfig{
		"offline": {Provider: "echo"},
	}, Auto: harness.AutoConfig{Enabled: true, MaxCandidates: 2}}
	check := doctorSelection(config, nil)
	if check.Status != "configured" {
		t.Fatalf("optional Auto settings were treated as missing models: %+v", check)
	}
}
