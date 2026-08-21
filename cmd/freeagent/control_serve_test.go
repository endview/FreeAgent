package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestControlServeFlagsDefaultOffNeverVerifyPrivilegeV1(t *testing.T) {
	original := verifyControlNonElevatedV1
	defer func() { verifyControlNonElevatedV1 = original }()
	calls := 0
	verifyControlNonElevatedV1 = func() error {
		calls++
		return errors.New("must not be called")
	}

	if err := validateControlServeFlagsV1(false, ""); err != nil {
		t.Fatalf("validate default-off Control flags: %v", err)
	}
	if calls != 0 {
		t.Fatalf("default-off Control flags performed %d privilege checks", calls)
	}
	if err := validateControlServeFlagsV1(false, "handoff.json"); err == nil ||
		!strings.Contains(err.Error(), "requires explicit --enable-control") {
		t.Fatalf("Control-only option error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("partial default-off flags performed %d privilege checks", calls)
	}
}

func TestControlServeFlagsFailBeforePrivilegeForInvalidPathV1(t *testing.T) {
	original := verifyControlNonElevatedV1
	defer func() { verifyControlNonElevatedV1 = original }()
	calls := 0
	verifyControlNonElevatedV1 = func() error {
		calls++
		return nil
	}

	for _, path := range []string{"", " ", " handoff.json", "handoff.json "} {
		if err := validateControlServeFlagsV1(true, path); err == nil {
			t.Fatalf("invalid handoff path %q was accepted", path)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid Control paths performed %d privilege checks", calls)
	}
	if err := validateControlServeFlagsV1(true, "handoff.json"); err != nil {
		t.Fatalf("validate exact Control flags: %v", err)
	}
	if calls != 1 {
		t.Fatalf("exact enabled flags performed %d privilege checks, want 1", calls)
	}
}

func TestRunServeDefaultOffAndPartialControlFailBeforeControlAccessV1(t *testing.T) {
	original := verifyControlNonElevatedV1
	defer func() { verifyControlNonElevatedV1 = original }()
	calls := 0
	verifyControlNonElevatedV1 = func() error {
		calls++
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runServe(
		context.Background(),
		[]string{"--db", t.TempDir() + "/missing.db"},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("default-off serve unexpectedly opened a missing Store")
	}
	if calls != 0 {
		t.Fatalf("default-off serve performed %d Control privilege checks", calls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("default-off failed serve published readiness: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runServe(
		context.Background(),
		[]string{
			"--db", t.TempDir() + "/missing.db",
			"--control-handoff-path", "handoff.json",
		},
		&stdout,
		&stderr,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"requires explicit --enable-control",
	) {
		t.Fatalf("partial Control serve error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("partial Control serve performed %d privilege checks", calls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("partial Control serve published readiness: %q", stdout.String())
	}
}

func TestCanonicalControlAuthorityRequiresExactIPv4LoopbackV1(t *testing.T) {
	authority, err := canonicalControlAuthorityV1(&net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 49152,
	})
	if err != nil || authority != "127.0.0.1:49152" {
		t.Fatalf("canonical Control authority = %q, %v", authority, err)
	}
	for _, address := range []net.Addr{
		&net.TCPAddr{IP: net.IPv4zero, Port: 49152},
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 49152},
		&net.TCPAddr{IP: net.IPv6loopback, Port: 49152},
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
		stringAddressV1("127.0.0.1:49152"),
	} {
		if value, err := canonicalControlAuthorityV1(address); err == nil || value != "" {
			t.Fatalf("noncanonical address %T(%v) accepted as %q", address, address, value)
		}
	}
}

type stringAddressV1 string

func (address stringAddressV1) Network() string { return "tcp" }
func (address stringAddressV1) String() string  { return string(address) }
