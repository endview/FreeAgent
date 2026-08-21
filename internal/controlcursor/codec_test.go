package controlcursor

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModulesCursorCodecCanonicalAndTokenCanaryV1(t *testing.T) {
	cursor := testCursorV1(t)
	payload, err := freezeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("freeze canary cursor")
	}
	defer clear(payload)
	const wantPayload = `{"authorization_revision":7,"boot_id":"boot-a","collection":"MODULES","filter_digest":"86dfc53738f821425ec77b3324c053c7f39ba54a02cb2c379a3cb04733fdc85e","last_instance_id":"instance-b","observed_at_unix_micros":1700000000000000,"position_digest":"e682af0ad450ab9e879deeacf2218a38bd8a41c360f8f6f7c1a70209489b3458","principal_id":"operator-a","schema_version":"control-modules-cursor/v1","scope":{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-a"},"scope_digest":"2a0abddf5d85fdd4ba6868257883425845161140cd3ecad566b5fc02a988e37e","scope_set_digest":"1111111111111111111111111111111111111111111111111111111111111111","sort_version":"control-modules-instance-id-binary/v1","source_digest":"2222222222222222222222222222222222222222222222222222222222222222","source_revision":11,"view_snapshot_digest":"3333333333333333333333333333333333333333333333333333333333333333"}`
	if string(payload) != wantPayload {
		t.Fatal("canonical payload canary changed")
	}

	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	if registry == nil {
		t.Fatal("create deterministic registry")
	}
	clear(key)
	defer registry.Close()
	envelope, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("encode canary cursor")
	}
	const wantEnvelope = "RkFDTQEABgOYYm9vdC1heyJhdXRob3JpemF0aW9uX3JldmlzaW9uIjo3LCJib290X2lkIjoiYm9vdC1hIiwiY29sbGVjdGlvbiI6Ik1PRFVMRVMiLCJmaWx0ZXJfZGlnZXN0IjoiODZkZmM1MzczOGY4MjE0MjVlYzc3YjMzMjRjMDUzYzdmMzliYTU0YTAyY2IyYzM3OWEzY2IwNDczM2ZkYzg1ZSIsImxhc3RfaW5zdGFuY2VfaWQiOiJpbnN0YW5jZS1iIiwib2JzZXJ2ZWRfYXRfdW5peF9taWNyb3MiOjE3MDAwMDAwMDAwMDAwMDAsInBvc2l0aW9uX2RpZ2VzdCI6ImU2ODJhZjBhZDQ1MGFiOWU4NzlkZWVhY2YyMjE4YTM4YmQ4YTQxYzM2MGY4ZjZmN2MxYTcwMjA5NDg5YjM0NTgiLCJwcmluY2lwYWxfaWQiOiJvcGVyYXRvci1hIiwic2NoZW1hX3ZlcnNpb24iOiJjb250cm9sLW1vZHVsZXMtY3Vyc29yL3YxIiwic2NvcGUiOnsia2luZCI6IldPUktTUEFDRSIsInNjaGVtYV92ZXJzaW9uIjoiY29udHJvbC1zY29wZS92MSIsInRlbmFudF9pZCI6InRlbmFudC1hIiwid29ya3NwYWNlX2lkIjoid29ya3NwYWNlLWEifSwic2NvcGVfZGlnZXN0IjoiMmEwYWJkZGY1ZDg1ZmRkNGJhNjg2ODI1Nzg4MzQyNTg0NTE2MTE0MGNkM2VjYWQ1NjZiNWZjMDJhOTg4ZTM3ZSIsInNjb3BlX3NldF9kaWdlc3QiOiIxMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExIiwic29ydF92ZXJzaW9uIjoiY29udHJvbC1tb2R1bGVzLWluc3RhbmNlLWlkLWJpbmFyeS92MSIsInNvdXJjZV9kaWdlc3QiOiIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyIiwic291cmNlX3JldmlzaW9uIjoxMSwidmlld19zbmFwc2hvdF9kaWdlc3QiOiIzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzIn0FH-rraFSSh_MnkTSFT0okGB6WHEnOda1F3pJEDrUWiw"
	if envelope != wantEnvelope {
		t.Fatal("envelope canary changed")
	}
	if len(envelope) == 0 || len(envelope) > MaximumModulesCursorTokenBytesV1 ||
		strings.Contains(envelope, "=") {
		t.Fatal("envelope is not bounded unpadded RawURL Base64")
	}
	decoded, err := registry.DecodeModulesCursorV1(envelope)
	if err != nil || !reflect.DeepEqual(decoded, cursor) {
		t.Fatal("canary envelope did not restore exact cursor")
	}
	second, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil || second != envelope {
		t.Fatal("same boot key and exact cursor did not produce stable envelope")
	}
}

func TestModulesCursorCodecRejectsMalformedTamperedAndWrongKeyV1(t *testing.T) {
	cursor := testCursorV1(t)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	if registry == nil {
		t.Fatal("create registry")
	}
	defer registry.Close()
	envelope, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("encode cursor")
	}

	tampered := []byte(envelope)
	if tampered[len(tampered)/2] == 'A' {
		tampered[len(tampered)/2] = 'B'
	} else {
		tampered[len(tampered)/2] = 'A'
	}
	wire, err := base64.RawURLEncoding.DecodeString(envelope)
	if err != nil {
		t.Fatal("decode test envelope")
	}
	badMagic := append([]byte(nil), wire...)
	badMagic[0] ^= 0xff
	badVersion := append([]byte(nil), wire...)
	badVersion[len(cursorMagicV1)]++
	badBootLengthZero := append([]byte(nil), wire...)
	binary.BigEndian.PutUint16(
		badBootLengthZero[len(cursorMagicV1)+1:len(cursorMagicV1)+3],
		0,
	)
	badBootLengthHigh := append([]byte(nil), wire...)
	binary.BigEndian.PutUint16(
		badBootLengthHigh[len(cursorMagicV1)+1:len(cursorMagicV1)+3],
		uint16(moduleapi.MaxOpaqueIDBytes+1),
	)
	badLength := append([]byte(nil), wire...)
	binary.BigEndian.PutUint16(
		badLength[len(cursorMagicV1)+3:cursorHeaderBytesV1],
		1,
	)
	badMAC := append([]byte(nil), wire...)
	badMAC[len(badMAC)-1] ^= 0xff
	clear(wire)

	tests := []string{
		"",
		"A",
		"+invalid",
		envelope + "=",
		envelope[:4] + "\n" + envelope[4:],
		envelope[:len(envelope)-1],
		string(tampered),
		base64.RawURLEncoding.EncodeToString(badMagic),
		base64.RawURLEncoding.EncodeToString(badVersion),
		base64.RawURLEncoding.EncodeToString(badBootLengthZero),
		base64.RawURLEncoding.EncodeToString(badBootLengthHigh),
		base64.RawURLEncoding.EncodeToString(badLength),
		base64.RawURLEncoding.EncodeToString(badMAC),
		strings.Repeat("A", MaximumModulesCursorTokenBytesV1+1),
	}
	clear(tampered)
	clear(badMagic)
	clear(badVersion)
	clear(badBootLengthZero)
	clear(badBootLengthHigh)
	clear(badLength)
	clear(badMAC)
	for index, candidate := range tests {
		if _, err := registry.DecodeModulesCursorV1(candidate); !errors.Is(
			err,
			ErrCursorInvalid,
		) {
			t.Fatalf("malformed case %d did not return uniform invalid", index)
		}
	}

	rotatedKey := testKeyV1(32)
	rotated := newRegistryWithKeyV1(cursor.BootID, rotatedKey)
	clear(rotatedKey)
	if rotated == nil {
		t.Fatal("create rotated registry")
	}
	defer rotated.Close()
	if _, err := rotated.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorInvalid,
	) {
		t.Fatal("envelope survived process-local key rotation")
	}
	restartedKey := testKeyV1(64)
	restarted := newRegistryWithKeyV1("boot-b", restartedKey)
	clear(restartedKey)
	if restarted == nil {
		t.Fatal("create restarted registry")
	}
	defer restarted.Close()
	_, restartErr := restarted.DecodeModulesCursorV1(envelope)
	if !errors.Is(restartErr, ErrCursorStale) ||
		!restarted.IsStaleModulesCursorErrorV1(restartErr) ||
		restarted.IsStaleModulesCursorErrorV1(ErrCursorInvalid) {
		t.Fatal("process restart did not produce exact stale classification")
	}
}

func TestModulesCursorCodecRejectsAuthenticatedNoncanonicalAndUnknownPayloadV1(
	t *testing.T,
) {
	cursor := testCursorV1(t)
	payload, err := freezeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("freeze cursor")
	}
	defer clear(payload)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	if registry == nil {
		t.Fatal("create registry")
	}
	defer registry.Close()

	unknown := bytes.Replace(
		payload,
		[]byte(`"view_snapshot_digest"`),
		[]byte(`"unknown":true,"view_snapshot_digest"`),
		1,
	)
	duplicate := bytes.Replace(
		payload,
		[]byte(`"boot_id":"boot-a"`),
		[]byte(`"boot_id":"boot-a","boot_id":"boot-a"`),
		1,
	)
	semanticInvalid := bytes.Replace(
		payload,
		[]byte(`"principal_id":"operator-a"`),
		[]byte(`"principal_id":""`),
		1,
	)
	tests := [][]byte{
		append([]byte{' '}, payload...),
		append(append([]byte(nil), payload...), []byte(`{}`)...),
		unknown,
		duplicate,
		semanticInvalid,
	}
	for index, candidate := range tests {
		envelope := testSealTokenV1(key, cursor.BootID, candidate)
		if _, err := registry.DecodeModulesCursorV1(envelope); !errors.Is(
			err,
			ErrCursorInvalid,
		) {
			t.Fatalf("authenticated invalid payload case %d was accepted", index)
		}
		clear(candidate)
	}
	clear(key)
}

func TestModulesCursorCodecBindsEnvelopeAndPayloadBootV1(t *testing.T) {
	cursor := testCursorV1(t)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	if registry == nil {
		t.Fatal("create registry")
	}
	defer registry.Close()

	foreignKey := testKeyV1(64)
	foreignEnvelope := testSealTokenV1(foreignKey, "boot-b", []byte{0xff})
	clear(foreignKey)
	foreignCursor, err := registry.DecodeModulesCursorV1(foreignEnvelope)
	if !errors.Is(err, ErrCursorStale) ||
		foreignCursor != (controlapp.DecodedModulesCursorV1{}) {
		t.Fatal("foreign boot did not short-circuit to stale with zero payload")
	}

	foreignPayloadCursor := cursor
	foreignPayloadCursor.BootID = "boot-b"
	foreignPayload, err := freezeModulesCursorV1(foreignPayloadCursor)
	if err != nil {
		t.Fatal("freeze foreign payload cursor")
	}
	defer clear(foreignPayload)
	mismatchedEnvelope := testSealTokenV1(key, cursor.BootID, foreignPayload)
	if _, err := registry.DecodeModulesCursorV1(mismatchedEnvelope); !errors.Is(
		err,
		ErrCursorInvalid,
	) {
		t.Fatal("authenticated envelope/payload boot mismatch was accepted")
	}
	clear(key)
}

func TestModulesCursorCodecMaximumValidCursorFitsTokenCeilingV1(t *testing.T) {
	cursor := testCursorV1(t)
	cursor.BootID = strings.Repeat("b", moduleapi.MaxOpaqueIDBytes)
	cursor.PrincipalID = strings.Repeat("p", moduleapi.MaxOpaqueIDBytes)
	cursor.Scope.TenantID = strings.Repeat("t", moduleapi.MaxOpaqueIDBytes)
	cursor.Scope.WorkspaceID = strings.Repeat("w", moduleapi.MaxOpaqueIDBytes)
	cursor.LastInstanceID = strings.Repeat("i", moduleapi.MaxOpaqueIDBytes)
	_, _, cursor.ScopeDigest = testScopeV1(t, cursor.Scope)
	cursor.PositionDigest = testPositionDigestV1(cursor.LastInstanceID)
	if err := cursor.Validate(); err != nil {
		t.Fatal("maximum-field cursor fixture is not valid")
	}
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	clear(key)
	if registry == nil {
		t.Fatal("create registry")
	}
	defer registry.Close()
	envelope, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("maximum valid cursor did not fit envelope format")
	}
	if len(envelope) > MaximumModulesCursorTokenBytesV1 {
		t.Fatal("maximum valid cursor exceeded envelope ceiling")
	}
	if decoded, err := registry.DecodeModulesCursorV1(envelope); err != nil ||
		!reflect.DeepEqual(decoded, cursor) {
		t.Fatal("maximum valid cursor did not round trip")
	}
}

func TestModulesCursorCodecCloseNilSafetyAndDefensiveKeyCopyV1(t *testing.T) {
	cursor := testCursorV1(t)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	if registry == nil {
		t.Fatal("create registry")
	}
	for index := range key {
		key[index] ^= 0xff
	}
	envelope, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("caller key mutation changed registry")
	}
	registry.Close()
	registry.Close()
	registry.mutex.RLock()
	zeroed := registry.key == [KeyBytesV1]byte{}
	bootCleared := registry.bootID == ""
	closed := registry.closed
	registry.mutex.RUnlock()
	if !zeroed || !bootCleared || !closed {
		t.Fatal("Close did not revoke and zero registry key")
	}
	if _, err := registry.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorStale,
	) {
		t.Fatal("closed registry accepted existing envelope")
	}
	if _, err := registry.EncodeModulesCursorV1(cursor); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatal("closed registry encoded cursor")
	}

	var typedNil *RegistryV1
	var codec ModulesCursorCodecV1 = typedNil
	if _, err := codec.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorInvalid,
	) {
		t.Fatal("typed-nil codec decode was not safe")
	}
	if _, err := codec.EncodeModulesCursorV1(cursor); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatal("typed-nil codec encode was not safe")
	}
	if !codec.IsStaleModulesCursorErrorV1(ErrCursorStale) ||
		codec.IsStaleModulesCursorErrorV1(ErrCursorInvalid) {
		t.Fatal("typed-nil codec classifier was not safe or exact")
	}
	typedNil.Close()

	invalid := cursor
	invalid.PrincipalID = ""
	freshKey := testKeyV1(0)
	fresh := newRegistryWithKeyV1(cursor.BootID, freshKey)
	clear(freshKey)
	if fresh == nil {
		t.Fatal("create fresh registry")
	}
	defer fresh.Close()
	if _, err := fresh.EncodeModulesCursorV1(invalid); !errors.Is(
		err,
		ErrCursorInvalid,
	) {
		t.Fatal("invalid decoded cursor was encoded")
	}
}

func TestModulesCursorCodecConcurrentCloseV1(t *testing.T) {
	cursor := testCursorV1(t)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	clear(key)
	if registry == nil {
		t.Fatal("create registry")
	}
	envelope, err := registry.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("encode cursor")
	}

	start := make(chan struct{})
	const workers = 32
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				_, encodeErr := registry.EncodeModulesCursorV1(cursor)
				if encodeErr != nil && !errors.Is(encodeErr, ErrUnavailable) {
					errorsChannel <- encodeErr
					return
				}
				_, decodeErr := registry.DecodeModulesCursorV1(envelope)
				if decodeErr != nil && !errors.Is(decodeErr, ErrCursorStale) {
					errorsChannel <- decodeErr
					return
				}
			}
		}()
	}
	close(start)
	var closeWait sync.WaitGroup
	for closer := 0; closer < 8; closer++ {
		closeWait.Add(1)
		go func() {
			defer closeWait.Done()
			registry.Close()
		}()
	}
	closeWait.Wait()
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal("concurrent Close produced an unexpected result")
		}
	}
	if _, err := registry.EncodeModulesCursorV1(cursor); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatal("encode succeeded after Close returned")
	}
	if _, err := registry.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorStale,
	) {
		t.Fatal("decode was not stale after Close returned")
	}
}

func TestModulesCursorCodecConcurrentRoundTripsV1(t *testing.T) {
	cursor := testCursorV1(t)
	key := testKeyV1(0)
	registry := newRegistryWithKeyV1(cursor.BootID, key)
	clear(key)
	if registry == nil {
		t.Fatal("create registry")
	}
	defer registry.Close()

	const workers = 32
	const iterations = 25
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				envelope, err := registry.EncodeModulesCursorV1(cursor)
				if err != nil {
					errorsChannel <- err
					return
				}
				decoded, err := registry.DecodeModulesCursorV1(envelope)
				if err != nil {
					errorsChannel <- err
					return
				}
				if !reflect.DeepEqual(decoded, cursor) {
					errorsChannel <- errors.New("decoded cursor changed")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal("concurrent round trip failed")
		}
	}
}

func TestNewRegistryV1UsesFreshProcessLocalKeysV1(t *testing.T) {
	first, err := NewRegistryV1("boot-a")
	if err != nil || first == nil {
		t.Fatal("create first CSPRNG registry")
	}
	defer first.Close()
	second, err := NewRegistryV1("boot-b")
	if err != nil || second == nil {
		t.Fatal("create second CSPRNG registry")
	}
	defer second.Close()
	cursor := testCursorV1(t)
	envelope, err := first.EncodeModulesCursorV1(cursor)
	if err != nil {
		t.Fatal("encode with first registry")
	}
	if _, err := second.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorStale,
	) {
		t.Fatal("fresh process-local registry accepted old envelope")
	}
	rotated, err := NewRegistryV1("boot-a")
	if err != nil || rotated == nil {
		t.Fatal("create same-boot rotated registry")
	}
	defer rotated.Close()
	if _, err := rotated.DecodeModulesCursorV1(envelope); !errors.Is(
		err,
		ErrCursorInvalid,
	) {
		t.Fatal("same-boot key rotation did not invalidate envelope")
	}
}

func TestNewRegistryV1RejectsInvalidBootIdentityV1(t *testing.T) {
	tests := []string{
		"",
		" boot",
		"boot ",
		"boot\nidentity",
		"e\u0301",
		strings.Repeat("b", moduleapi.MaxOpaqueIDBytes+1),
	}
	for index, bootID := range tests {
		registry, err := NewRegistryV1(bootID)
		if registry != nil || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid boot case %d was accepted", index)
		}
	}
	if newRegistryWithKeyV1("boot-a", make([]byte, KeyBytesV1-1)) != nil {
		t.Fatal("invalid deterministic key length was accepted")
	}
}

func testCursorV1(t *testing.T) controlapp.DecodedModulesCursorV1 {
	t.Helper()
	scope, _, scopeDigest := testScopeV1(t, controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeWorkspaceV1,
		TenantID:      "tenant-a",
		WorkspaceID:   "workspace-a",
	})
	return controlapp.DecodedModulesCursorV1{
		SchemaVersion:         controlapp.ModulesCursorSchemaVersionV1,
		BootID:                "boot-a",
		PrincipalID:           "operator-a",
		AuthorizationRevision: 7,
		ScopeSetDigest:        strings.Repeat("1", moduleapi.SHA256HexLength),
		Scope:                 scope,
		ScopeDigest:           scopeDigest,
		Collection:            controlapicontract.PageCollectionModulesV1,
		FilterDigest: moduleapi.Digest(
			"freeagent.control-modules-empty-filter/v1",
			[]byte(`{"filters":[]}`),
		),
		SortVersion:          controlapp.ModulesSortVersionV1,
		SourceRevision:       11,
		SourceDigest:         strings.Repeat("2", moduleapi.SHA256HexLength),
		ViewSnapshotDigest:   strings.Repeat("3", moduleapi.SHA256HexLength),
		ObservedAtUnixMicros: 1_700_000_000_000_000,
		LastInstanceID:       "instance-b",
		PositionDigest:       testPositionDigestV1("instance-b"),
	}
}

func testScopeV1(
	t *testing.T,
	scope controlapicontract.ControlScopeV1,
) (controlapicontract.ControlScopeV1, []byte, string) {
	t.Helper()
	frozen, canonical, digest, err := controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		t.Fatal("freeze test scope")
	}
	return frozen, canonical, digest
}

func testPositionDigestV1(lastInstanceID string) string {
	canonical, err := moduleapi.CanonicalJSON([]byte(
		`{"sort_version":"` + controlapp.ModulesSortVersionV1 +
			`","last_instance_id":"` + lastInstanceID + `"}`,
	))
	if err != nil {
		panic("invalid static position fixture")
	}
	return moduleapi.Digest(
		"freeagent.control-modules-position/v1",
		canonical,
	)
}

func testKeyV1(offset byte) []byte {
	key := make([]byte, KeyBytesV1)
	for index := range key {
		key[index] = byte(index) + offset
	}
	return key
}

func testSealTokenV1(key []byte, bootID string, payload []byte) string {
	wire := sealV1(key, bootID, payload)
	defer clear(wire)
	return base64.RawURLEncoding.EncodeToString(wire)
}
