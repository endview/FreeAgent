//go:build windows

package currentbackup

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPublishNoReplaceWindowsRetriesTransientFailures(t *testing.T) {
	const (
		source      = "C:" + `\source`
		destination = "C:" + `\destination`
	)
	failures := []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		nil,
	}
	var (
		attempts                int
		firstSourcePointer      *uint16
		firstDestinationPointer *uint16
		pointersStable          = true
		pathsStable             = true
		flagsStable             = true
		delays                  []time.Duration
	)
	err := publishNoReplaceWindows(
		source,
		destination,
		func(sourcePointer, destinationPointer *uint16, flags uint32) error {
			if attempts == 0 {
				firstSourcePointer = sourcePointer
				firstDestinationPointer = destinationPointer
			} else if sourcePointer != firstSourcePointer ||
				destinationPointer != firstDestinationPointer {
				pointersStable = false
			}
			if windows.UTF16PtrToString(sourcePointer) != source ||
				windows.UTF16PtrToString(destinationPointer) != destination {
				pathsStable = false
			}
			if flags != windows.MOVEFILE_WRITE_THROUGH {
				flagsStable = false
			}
			failure := failures[attempts]
			attempts++
			return failure
		},
		func(delay time.Duration) {
			delays = append(delays, delay)
		},
	)
	if err != nil {
		t.Fatalf("publish after transient failures: %v", err)
	}
	if attempts != len(failures) {
		t.Fatalf("MoveFileEx attempts = %d, want %d", attempts, len(failures))
	}
	if firstSourcePointer == nil || firstDestinationPointer == nil ||
		!pointersStable || !pathsStable {
		t.Fatal("MoveFileEx source or destination changed between retries")
	}
	if !flagsStable {
		t.Fatal("MoveFileEx flags changed from MOVEFILE_WRITE_THROUGH")
	}
	wantDelays := []time.Duration{
		currentBackupWindowsPublishInitialRetryDelay,
		2 * currentBackupWindowsPublishInitialRetryDelay,
	}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
}

func TestPublishNoReplaceWindowsExhaustsTransientRetryBudget(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	err := publishNoReplaceWindows(
		"C:"+`\source`,
		"C:"+`\destination`,
		func(_, _ *uint16, flags uint32) error {
			attempts++
			if flags != windows.MOVEFILE_WRITE_THROUGH {
				t.Fatalf("MoveFileEx flags = %#x", flags)
			}
			if attempts == currentBackupWindowsPublishRetryCount+1 {
				return windows.ERROR_SHARING_VIOLATION
			}
			return windows.ERROR_ACCESS_DENIED
		},
		func(delay time.Duration) {
			delays = append(delays, delay)
		},
	)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("exhausted retry error = %v", err)
	}
	if attempts != currentBackupWindowsPublishRetryCount+1 {
		t.Fatalf(
			"MoveFileEx attempts = %d, want %d",
			attempts,
			currentBackupWindowsPublishRetryCount+1,
		)
	}
	wantDelays := make([]time.Duration, currentBackupWindowsPublishRetryCount)
	for retry := range wantDelays {
		wantDelays[retry] = currentBackupWindowsPublishInitialRetryDelay << retry
	}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	var total time.Duration
	for _, delay := range delays {
		total += delay
	}
	if total != 630*time.Millisecond {
		t.Fatalf("total retry delay = %v, want 630ms", total)
	}
}

func TestPublishNoReplaceWindowsDoesNotRetryTerminalFailures(t *testing.T) {
	tests := []struct {
		name          string
		failure       error
		wantCollision bool
	}{
		{name: "already exists", failure: windows.ERROR_ALREADY_EXISTS, wantCollision: true},
		{name: "file exists", failure: windows.ERROR_FILE_EXISTS, wantCollision: true},
		{
			name: "joined collision wins",
			failure: errors.Join(
				windows.ERROR_ACCESS_DENIED,
				windows.ERROR_ALREADY_EXISTS,
			),
			wantCollision: true,
		},
		{name: "other", failure: windows.ERROR_INVALID_PARAMETER},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			sleeps := 0
			err := publishNoReplaceWindows(
				"C:"+`\source`,
				"C:"+`\destination`,
				func(_, _ *uint16, flags uint32) error {
					attempts++
					if flags != windows.MOVEFILE_WRITE_THROUGH {
						t.Fatalf("MoveFileEx flags = %#x", flags)
					}
					return test.failure
				},
				func(time.Duration) {
					sleeps++
				},
			)
			if attempts != 1 || sleeps != 0 {
				t.Fatalf("terminal failure attempts=%d sleeps=%d", attempts, sleeps)
			}
			if test.wantCollision {
				if !errors.Is(err, ErrTargetExists) {
					t.Fatalf("collision error = %v, want %v", err, ErrTargetExists)
				}
				return
			}
			if !errors.Is(err, test.failure) {
				t.Fatalf("terminal error = %v, want %v", err, test.failure)
			}
		})
	}
}
