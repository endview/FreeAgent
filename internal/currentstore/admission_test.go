package currentstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResolveAdmissionNotFoundAndConflictingIntent(t *testing.T) {
	store := openContentTestStore(t)
	result, found, err := store.ResolveAdmission(
		context.Background(),
		"tenant-1",
		"admission-1",
		strings.Repeat("a", 64),
	)
	if err != nil || found || result != (RunAdmissionResult{}) {
		t.Fatalf(
			"missing ResolveAdmission=(%+v,%v,%v)",
			result,
			found,
			err,
		)
	}
	now := time.Now().UTC().UnixMicro()
	execClosedFileTamperV1(
		t,
		store,
		nil,
		`
		INSERT INTO runs(
			run_id, tenant_id, workspace_id, admission_key,
			admission_intent_digest, state, disposition,
			revision, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, NULL, 0, ?, ?)
		`,
		"run-1",
		"tenant-1",
		"workspace-1",
		"admission-1",
		strings.Repeat("a", 64),
		"ADMITTED",
		now,
		now,
	)
	if _, found, err := store.ResolveAdmission(
		context.Background(),
		"tenant-1",
		"admission-1",
		strings.Repeat("b", 64),
	); !found || !errors.Is(err, ErrAdmissionConflict) {
		t.Fatalf("conflicting ResolveAdmission found=%v err=%v", found, err)
	}
	if _, found, err := store.ResolveAdmission(
		context.Background(),
		"tenant-1",
		"admission-1",
		strings.Repeat("a", 64),
	); !found || !errors.Is(err, ErrAdmissionIntegrity) {
		t.Fatalf("partial closure found=%v err=%v", found, err)
	}
}

func TestResolveAdmissionValidatesInputAndStoreLifecycle(t *testing.T) {
	var nilStore *Store
	if _, _, err := nilStore.ResolveAdmission(
		context.Background(),
		"tenant",
		"key",
		strings.Repeat("a", 64),
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("nil Store error=%v", err)
	}
	store := openContentTestStore(t)
	if _, _, err := store.ResolveAdmission(
		context.Background(),
		" tenant",
		"key",
		strings.Repeat("a", 64),
	); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("invalid tenant error=%v", err)
	}
	if _, _, err := store.ResolveAdmission(
		context.Background(),
		"tenant",
		"key",
		"not-a-digest",
	); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("invalid digest error=%v", err)
	}
}
