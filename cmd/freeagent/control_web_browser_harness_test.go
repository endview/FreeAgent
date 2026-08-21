package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestControlWebRealBrowserHarnessV1 is an opt-in test-only launcher for the
// real-browser release check. It keeps the production composition, Store,
// handlers, and embedded assets intact while replacing only the Windows
// elevation and owner-only handoff delivery seams already exercised by the
// production HTTP E2E. Ordinary go test runs skip it.
func TestControlWebRealBrowserHarnessV1(t *testing.T) {
	readyPath := os.Getenv("FREEAGENT_BROWSER_E2E_READY_PATH")
	stopPath := os.Getenv("FREEAGENT_BROWSER_E2E_STOP_PATH")
	if readyPath == "" || stopPath == "" {
		t.Skip("real-browser harness is opt-in")
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePlan := newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0)
	eligibleEnablePlan := bytes.Replace(
		enablePlan,
		[]byte(`"failure_policy":"REQUIRED"`),
		[]byte(`"failure_policy":"OPTIONAL"`),
		1,
	)
	if bytes.Equal(eligibleEnablePlan, enablePlan) {
		t.Fatal("failed to construct browser-harness OPTIONAL module fixture")
	}
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		eligibleEnablePlan,
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare browser-harness MODULE_DISABLE: result=%+v err=%v", result, err)
	}
	handoffPath := filepath.Join(root, "control-handoff.json")

	originalVerifier := verifyControlNonElevatedV1
	verifyControlNonElevatedV1 = func() error { return nil }
	t.Cleanup(func() { verifyControlNonElevatedV1 = originalVerifier })
	installControlServeE2EHandoffPublisherV1(t, handoffPath)

	serveContext, stopServe := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	serveDone := make(chan error, 1)
	go func() {
		err := run(
			serveContext,
			[]string{
				"serve",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--tenant", defaultTenantID,
				"--listen", "127.0.0.1:0",
				"--enable-control",
				"--control-handoff-path", handoffPath,
			},
			stdoutWriter,
			io.Discard,
		)
		_ = stdoutWriter.CloseWithError(err)
		serveDone <- err
	}()
	defer stopServe()

	var readiness map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&readiness); err != nil {
		t.Fatalf("decode browser-harness readiness: %v", err)
	}
	if readiness["status"] != "ready" || readiness["control"] != "enabled" {
		t.Fatalf("browser-harness readiness=%v", readiness)
	}
	handoffCanonical, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("read browser-harness handoff: %v", err)
	}
	var handoff controlServeE2EHandoffV1
	decodeControlServeE2ESecretJSONV1(t, handoffCanonical, &handoff)
	clear(handoffCanonical)

	launchCanonical, err := json.Marshal(map[string]string{
		"handoff_path": handoffPath,
		"instance_id":  fixture.InstanceID,
		"origin":       handoff.Origin,
		"profile_id":   moduleApplyTestProfileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyPath, launchCanonical, 0o600); err != nil {
		t.Fatalf("write browser-harness launch file: %v", err)
	}

	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-serveDone:
			t.Fatalf("browser-harness serve stopped early: %v", err)
		case <-deadline.C:
			t.Fatal("browser-harness completion timed out")
		case <-ticker.C:
			if _, err := os.Stat(stopPath); err == nil {
				stopServe()
				select {
				case err := <-serveDone:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Fatalf("browser-harness shutdown: %v", err)
					}
					return
				case <-time.After(15 * time.Second):
					t.Fatal("browser-harness shutdown timed out")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("inspect browser-harness completion: %v", err)
			}
		}
	}
}
