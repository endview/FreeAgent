package controlapipolicy

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPageQueryV1UsesContractShapeAndFrozenDefault(t *testing.T) {
	input := controlapicontract.PageQueryV1{}
	got, err := NormalizePageQueryV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Limit != DefaultPageSizeV1 || got.After != nil || got.FilterDigest != "" {
		t.Fatalf("normalized query = %#v", got)
	}
	digest := strings.Repeat("a", moduleapi.SHA256HexLength)
	after := &controlapicontract.PageTokenRefV1{
		ScopeDigest:        digest,
		ViewSnapshotDigest: digest,
		Collection:         controlapicontract.PageCollectionRunsV1,
		PositionDigest:     digest,
	}
	got, err = NormalizePageQueryV1(controlapicontract.PageQueryV1{
		Limit:        MaximumPageSizeV1,
		After:        after,
		FilterDigest: digest,
	})
	if err != nil || got.Limit != MaximumPageSizeV1 ||
		got.After == nil || got.After.PositionDigest != digest ||
		got.FilterDigest != digest {
		t.Fatalf("maximum query = %#v, err = %v", got, err)
	}
	after.PositionDigest = strings.Repeat("b", moduleapi.SHA256HexLength)
	if got.After.PositionDigest != digest {
		t.Fatal("normalized page token reference aliases caller memory")
	}
	invalid := []controlapicontract.PageQueryV1{
		{Limit: MaximumPageSizeV1 + 1},
		{Limit: 1, After: &controlapicontract.PageTokenRefV1{}},
		{Limit: 1, FilterDigest: "opaque"},
	}
	for index, query := range invalid {
		if _, err := NormalizePageQueryV1(query); err == nil {
			t.Fatalf("invalid page query %d was accepted: %#v", index, query)
		}
	}
}

func TestFilterTermsV1AreCanonicalBoundedAndDefensivelyCopied(t *testing.T) {
	input := []FilterTermV1{
		{Name: "state", Value: "UNKNOWN"},
		{Name: "workspace_id", Value: "workspace-1"},
	}
	got, digest, err := DigestFilterTermsV1(input, []string{"state", "workspace_id"})
	if err != nil || !moduleapi.ValidSHA256(digest) || !reflect.DeepEqual(got, input) {
		t.Fatalf("filter terms = %#v, digest = %q, err = %v", got, digest, err)
	}
	input[0].Value = "MUTATED"
	if got[0].Value != "UNKNOWN" {
		t.Fatal("validated filter terms alias caller memory")
	}
	_, retryDigest, err := DigestFilterTermsV1(got, []string{"state", "workspace_id"})
	if err != nil || retryDigest != digest {
		t.Fatal("canonical filter digest changed on exact retry")
	}
	invalid := [][]FilterTermV1{
		{{Name: "unknown", Value: "x"}},
		{{Name: "workspace_id", Value: " workspace-1"}},
		{
			{Name: "workspace_id", Value: "one"},
			{Name: "state", Value: "UNKNOWN"},
		},
		{
			{Name: "state", Value: "UNKNOWN"},
			{Name: "state", Value: "FAILED"},
		},
	}
	for index, filters := range invalid {
		if _, _, err := DigestFilterTermsV1(filters, []string{"state", "workspace_id"}); err == nil {
			t.Fatalf("invalid filter set %d was accepted: %#v", index, filters)
		}
	}
	if _, _, err := DigestFilterTermsV1(nil, []string{"workspace_id", "state"}); err == nil {
		t.Fatal("non-canonical endpoint filter allowlist was accepted")
	}
}

func TestKeysetCursorSpecV1IsFrozenPolicyNotCodec(t *testing.T) {
	spec := DefaultKeysetCursorSpecV1()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.SchemaVersion != PageCursorSchemaVersionV1 ||
		spec.Encoding != "BASE64URL_NO_PADDING" ||
		spec.Protection != "AUTHENTICATED_OPAQUE" ||
		!spec.BindsBootID || !spec.BindsPrincipal || !spec.BindsScope ||
		!spec.BindsResourceKind || !spec.BindsFilters || !spec.BindsOrdering ||
		!spec.BindsSortVersion || !spec.BindsSourceRevision || !spec.BindsLastKey ||
		!spec.CarriesLastKeyOnly || !spec.ForbidsOffsetPaging {
		t.Fatalf("incomplete keyset cursor spec: %#v", spec)
	}
	spec.BindsScope = false
	if err := spec.Validate(); err == nil {
		t.Fatal("weakened cursor spec was accepted")
	}
}

func TestIdempotencyKeyDigestPreservesEveryRawByteWithoutLeakingIt(t *testing.T) {
	raw := "  Case-Sensitive-Key  "
	digest, err := DigestIdempotencyKeyV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := moduleapi.Digest(IdempotencyKeyDigestDomainV1, []byte(raw))
	if digest != want || !moduleapi.ValidSHA256(digest) {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
	if strings.Contains(digest, raw) {
		t.Fatal("raw idempotency key leaked into digest")
	}
	caseChanged, err := DigestIdempotencyKeyV1(strings.ToLower(raw))
	if err != nil || caseChanged == digest {
		t.Fatal("idempotency key was case-folded")
	}
	trimmed, err := DigestIdempotencyKeyV1(strings.TrimSpace(raw))
	if err != nil || trimmed == digest {
		t.Fatal("idempotency key was trimmed")
	}

	for _, invalid := range []string{
		"too-short",
		strings.Repeat("x", MaximumIdempotencyKeyBytesV1+1),
		"sixteen-bytes-ok\n",
		"含有非ASCII的密钥-123456",
	} {
		_, invalidErr := DigestIdempotencyKeyV1(invalid)
		if invalidErr == nil {
			t.Fatalf("invalid idempotency key %q was accepted", invalid)
		}
		if strings.Contains(invalidErr.Error(), invalid) {
			t.Fatal("raw invalid idempotency key leaked into error")
		}
	}
}

func TestOperationBodyDigestIsCanonicalBoundedAndDomainSeparated(t *testing.T) {
	canonical, first, err := DigestOperationBodyV1(
		controlapicontract.OperationModuleDisableV1,
		json.RawMessage(`{"z":1,"a":{"value":true}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"a":{"value":true},"z":1}` {
		t.Fatalf("canonical body = %s", canonical)
	}
	canonical[0] = '['
	secondCanonical, second, err := DigestOperationBodyV1(
		controlapicontract.OperationModuleDisableV1,
		json.RawMessage(`{"a":{"value":true},"z":1}`),
	)
	if err != nil || first != second || secondCanonical[0] != '{' {
		t.Fatalf("canonical retry changed: digest %q/%q, body %s, err %v", first, second, secondCanonical, err)
	}
	_, otherOperation, err := DigestOperationBodyV1(
		controlapicontract.OperationModuleApplyV1,
		json.RawMessage(`{"a":{"value":true},"z":1}`),
	)
	if err != nil || otherOperation == first {
		t.Fatal("operation domains were not separated")
	}
	for _, test := range []struct {
		operation string
		body      json.RawMessage
	}{
		{"module_disable", json.RawMessage(`{}`)},
		{" MODULE_DISABLE", json.RawMessage(`{}`)},
		{"MODULE_DISABLE", json.RawMessage(`[]`)},
		{"MODULE_DISABLE", json.RawMessage(`{"duplicate":1,"duplicate":2}`)},
		{"MODULE_DISABLE", json.RawMessage(strings.Repeat(" ", MaximumOperationBodyBytesV1+1))},
	} {
		if _, _, err := DigestOperationBodyV1(
			controlapicontract.ControlOperationV1(test.operation),
			test.body,
		); err == nil {
			t.Fatalf("invalid operation body was accepted: %q %s", test.operation, test.body)
		}
	}
}

func TestStrongETagV1BindsCompleteExpectedRef(t *testing.T) {
	ref := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: "snapshot-1",
		Revision:   7,
		Digest:     strings.Repeat("a", moduleapi.SHA256HexLength),
	}
	etag, err := StrongETagV1(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) ||
		len(etag) != moduleapi.SHA256HexLength+2 {
		t.Fatalf("ETag is not one strong quoted validator: %q", etag)
	}
	if err := ValidateIfMatchV1(etag, ref); err != nil {
		t.Fatal(err)
	}
	for _, mismatch := range []string{"", "*", "W/" + etag, etag + "," + etag, " " + etag} {
		if err := ValidateIfMatchV1(mismatch, ref); err == nil {
			t.Fatalf("invalid If-Match %q was accepted", mismatch)
		}
	}
	changed := ref
	changed.Digest = strings.Repeat("b", moduleapi.SHA256HexLength)
	if err := ValidateIfMatchV1(etag, changed); err == nil {
		t.Fatal("body expected digest drift matched the prior ETag")
	}
}

func TestThreatModelV1FreezesLoopbackSessionAndResourceLimits(t *testing.T) {
	model := DefaultThreatModelV1()
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	if model.ListenerNetwork != "tcp4" || model.ListenerAddress != "127.0.0.1:0" ||
		model.MaximumRequestBodyBytes != 1<<20 ||
		model.MaximumBootstrapBodyBytes != 4<<10 ||
		model.MaximumRequestTargetBytes != 8<<10 ||
		model.MaximumResponseBodyBytes != 1<<20 ||
		model.MaximumHeaderBytes != 16<<10 || model.MaximumHeaderCount != 64 ||
		model.MaximumGlobalConcurrency != 32 ||
		model.MaximumSessionConcurrency != 8 ||
		!model.BootstrapSingleUse || !model.BootstrapNotDurablyPersisted ||
		!model.BootstrapOwnerOnlyHandoff ||
		!model.BootstrapHandoffExclusiveCreate ||
		!model.BootstrapRegistryDigestOnly ||
		model.BootstrapBytes != 32 || MaximumBootstrapBodyBytesV1 != 4<<10 ||
		!model.SessionMemoryOnly || !model.SessionCookieHTTPOnly ||
		!model.SessionSameSiteStrict || !model.SessionHostOnly ||
		model.SessionCookiePath != "/control/" ||
		model.SessionAbsoluteMillis != 8*60*60*1_000 ||
		model.SessionIdleMillis != 30*60*1_000 ||
		!model.RequireSessionBoundCSRF || !model.ForbidClientActorIdentity ||
		!model.ForbidSecretsInWire || !model.ForbidSecretsInStore ||
		!model.ForbidSecretsInBackup || !model.ForbidSecretsInErrors {
		t.Fatalf("incomplete threat model: %#v", model)
	}
	mutated := model
	mutated.MaximumRequestBodyBytes++
	if err := mutated.Validate(); err == nil {
		t.Fatal("mutated resource limit was accepted")
	}
	mutated = model
	mutated.SessionSameSiteStrict = false
	if err := mutated.Validate(); err == nil {
		t.Fatal("weakened session policy was accepted")
	}
	if model.ContentSecurityPolicy != ControlContentSecurityPolicyV1 ||
		strings.Contains(model.ContentSecurityPolicy, "'unsafe-inline'") ||
		strings.Contains(model.ContentSecurityPolicy, "*") ||
		!strings.Contains(model.ContentSecurityPolicy, "font-src 'none'") ||
		!model.ForbidBootstrapRawInContractWire ||
		!model.ForbidSessionRawInContractWire ||
		!model.ForbidCSRFRawInContractWire ||
		!model.ForbidForwardedAuthority || !model.ForbidMethodOverride ||
		!model.ForbidMultipartAndForm || !model.ForbidImplicitRedirect ||
		!model.RequireDeclaredMethod || !model.RequireApplicationJSON ||
		!model.RequireNoSniff || !model.RequireNoReferrer ||
		!model.RequireFrameDeny || !model.ForbidUnboundedQueue {
		t.Fatalf("incomplete CSP or credential-wire policy: %#v", model)
	}
}

func TestLoopbackAuthorityAndOriginAreExact(t *testing.T) {
	if err := ValidateListenerConfigurationV1("tcp4", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	for _, configuration := range [][2]string{
		{"tcp", "127.0.0.1:0"},
		{"tcp4", "127.0.0.1:8080"},
		{"tcp6", "[::1]:0"},
		{"tcp4", "localhost:0"},
	} {
		if err := ValidateListenerConfigurationV1(configuration[0], configuration[1]); err == nil {
			t.Fatalf("listener configuration %#v was accepted", configuration)
		}
	}

	const authority = "127.0.0.1:43127"
	if err := ValidateBoundAuthorityV1(authority); err != nil {
		t.Fatal(err)
	}
	origin, err := ExactOriginV1(authority)
	if err != nil || origin != "http://"+authority {
		t.Fatalf("origin = %q, err = %v", origin, err)
	}
	if err := ValidateAuthorityAndOriginV1(authority, authority, origin); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"localhost:43127",
		"127.0.0.1:0",
		"127.0.0.1:043127",
		"127.0.0.1:65536",
		"127.0.0.1:43127 ",
		"[::1]:43127",
	} {
		if err := ValidateBoundAuthorityV1(invalid); err == nil {
			t.Fatalf("invalid authority %q was accepted", invalid)
		}
	}
	if err := ValidateAuthorityAndOriginV1(authority, "LOCALHOST:43127", origin); err == nil {
		t.Fatal("non-exact Host was accepted")
	}
	if err := ValidateAuthorityAndOriginV1(authority, authority, "null"); err == nil {
		t.Fatal("non-exact Origin was accepted")
	}
	if err := ValidateAuthorityAndOriginV1(authority, authority, ""); err == nil {
		t.Fatal("missing Origin was accepted")
	}
}

func TestProductionPackageDirectDependenciesStayPure(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(
		goBinary,
		"list",
		"-f",
		`{{join .Imports "\n"}}`,
		".",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list direct imports: %v", err)
	}
	forbidden := map[string]struct{}{
		"database/sql":  {},
		"net/http":      {},
		"os":            {},
		"path/filepath": {},
		"github.com/endview/freeagent/internal/currentstore": {},
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/controlapicontract": {},
		"github.com/endview/freeagent/sdk/moduleapi":               {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if _, denied := forbidden[dependency]; denied {
			t.Fatalf("pure policy package imports forbidden dependency %q", dependency)
		}
		if strings.HasPrefix(dependency, projectPrefix) {
			if _, allowed := allowedProject[dependency]; !allowed {
				t.Fatalf("pure policy package imports unapproved project dependency %q", dependency)
			}
		}
	}
}
