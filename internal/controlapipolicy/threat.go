package controlapipolicy

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	ThreatModelSchemaVersionV1 = "control-api-threat-model/v1"

	ControlListenerNetworkV1       = "tcp4"
	ControlListenerAddressV1       = "127.0.0.1:0"
	ControlOriginSchemeV1          = "http://"
	ControlContentSecurityPolicyV1 = "default-src 'self'; base-uri 'none'; " +
		"object-src 'none'; frame-ancestors 'none'; form-action 'none'; " +
		"connect-src 'self'; img-src 'self' data:; script-src 'self'; " +
		"style-src 'self'; font-src 'none'"

	MaximumBootstrapBodyBytesV1 = 4 << 10
	MaximumRequestTargetBytesV1 = 8 << 10
	MaximumRequestBodyBytesV1   = 1 << 20
	MaximumResponseBodyBytesV1  = 1 << 20
	MaximumHeaderBytesV1        = 16 << 10
	MaximumHeaderCountV1        = 64
	MaximumGlobalConcurrencyV1  = 32
	MaximumSessionConcurrencyV1 = 8

	ReadHeaderTimeoutMillisV1 = 5_000
	RequestTimeoutMillisV1    = 30_000
	IdleTimeoutMillisV1       = 60_000
	ShutdownTimeoutMillisV1   = 30_000

	BootstrapCapabilityBytesV1 = 32
	BootstrapLifetimeMillisV1  = 5 * 60 * 1_000
	SessionIdentifierBytesV1   = 32
	SessionAbsoluteMillisV1    = 8 * 60 * 60 * 1_000
	SessionIdleMillisV1        = 30 * 60 * 1_000
	CSRFTokenBytesV1           = 32
)

// ThreatModelV1 is immutable policy data. Validate does not bind sockets,
// create credentials, create a handoff file, enforce an owner ACL, inspect
// processes, or mutate runtime state. Those are later adapter obligations.
type ThreatModelV1 struct {
	SchemaVersion string

	ListenerNetwork       string
	ListenerAddress       string
	OriginScheme          string
	ContentSecurityPolicy string

	RequireExactHost   bool
	RequireExactOrigin bool
	ForbidRemoteListen bool
	ForbidWildcardCORS bool

	BootstrapSingleUse              bool
	BootstrapNotDurablyPersisted    bool
	BootstrapOwnerOnlyHandoff       bool
	BootstrapHandoffExclusiveCreate bool
	BootstrapRegistryDigestOnly     bool
	BootstrapBytes                  int
	BootstrapLifetimeMillis         int

	SessionMemoryOnly      bool
	SessionCookieHTTPOnly  bool
	SessionSameSiteStrict  bool
	SessionHostOnly        bool
	SessionCookiePath      string
	SessionIdentifierBytes int
	SessionAbsoluteMillis  int
	SessionIdleMillis      int

	RequireSessionBoundCSRF bool
	CSRFTokenBytes          int

	MaximumRequestBodyBytes   int
	MaximumBootstrapBodyBytes int
	MaximumRequestTargetBytes int
	MaximumResponseBodyBytes  int
	MaximumHeaderBytes        int
	MaximumHeaderCount        int
	MaximumGlobalConcurrency  int
	MaximumSessionConcurrency int
	ReadHeaderTimeoutMillis   int
	RequestTimeoutMillis      int
	IdleTimeoutMillis         int
	ShutdownTimeoutMillis     int

	ForbidClientActorIdentity        bool
	ForbidSecretsInWire              bool
	ForbidSecretsInURL               bool
	ForbidSecretsInLogs              bool
	ForbidSecretsInStore             bool
	ForbidSecretsInBackup            bool
	ForbidSecretsInErrors            bool
	ForbidPublicTLSTermination       bool
	ForbidBootstrapRawInContractWire bool
	ForbidSessionRawInContractWire   bool
	ForbidCSRFRawInContractWire      bool
	ForbidForwardedAuthority         bool
	ForbidMethodOverride             bool
	ForbidMultipartAndForm           bool
	ForbidImplicitRedirect           bool
	RequireDeclaredMethod            bool
	RequireApplicationJSON           bool
	RequireNoSniff                   bool
	RequireNoReferrer                bool
	RequireFrameDeny                 bool
	ForbidUnboundedQueue             bool
}

func DefaultThreatModelV1() ThreatModelV1 {
	return ThreatModelV1{
		SchemaVersion: ThreatModelSchemaVersionV1,

		ListenerNetwork:       ControlListenerNetworkV1,
		ListenerAddress:       ControlListenerAddressV1,
		OriginScheme:          ControlOriginSchemeV1,
		ContentSecurityPolicy: ControlContentSecurityPolicyV1,

		RequireExactHost:   true,
		RequireExactOrigin: true,
		ForbidRemoteListen: true,
		ForbidWildcardCORS: true,

		BootstrapSingleUse:              true,
		BootstrapNotDurablyPersisted:    true,
		BootstrapOwnerOnlyHandoff:       true,
		BootstrapHandoffExclusiveCreate: true,
		BootstrapRegistryDigestOnly:     true,
		BootstrapBytes:                  BootstrapCapabilityBytesV1,
		BootstrapLifetimeMillis:         BootstrapLifetimeMillisV1,

		SessionMemoryOnly:      true,
		SessionCookieHTTPOnly:  true,
		SessionSameSiteStrict:  true,
		SessionHostOnly:        true,
		SessionCookiePath:      "/control/",
		SessionIdentifierBytes: SessionIdentifierBytesV1,
		SessionAbsoluteMillis:  SessionAbsoluteMillisV1,
		SessionIdleMillis:      SessionIdleMillisV1,

		RequireSessionBoundCSRF: true,
		CSRFTokenBytes:          CSRFTokenBytesV1,

		MaximumRequestBodyBytes:   MaximumRequestBodyBytesV1,
		MaximumBootstrapBodyBytes: MaximumBootstrapBodyBytesV1,
		MaximumRequestTargetBytes: MaximumRequestTargetBytesV1,
		MaximumResponseBodyBytes:  MaximumResponseBodyBytesV1,
		MaximumHeaderBytes:        MaximumHeaderBytesV1,
		MaximumHeaderCount:        MaximumHeaderCountV1,
		MaximumGlobalConcurrency:  MaximumGlobalConcurrencyV1,
		MaximumSessionConcurrency: MaximumSessionConcurrencyV1,
		ReadHeaderTimeoutMillis:   ReadHeaderTimeoutMillisV1,
		RequestTimeoutMillis:      RequestTimeoutMillisV1,
		IdleTimeoutMillis:         IdleTimeoutMillisV1,
		ShutdownTimeoutMillis:     ShutdownTimeoutMillisV1,

		ForbidClientActorIdentity:        true,
		ForbidSecretsInWire:              true,
		ForbidSecretsInURL:               true,
		ForbidSecretsInLogs:              true,
		ForbidSecretsInStore:             true,
		ForbidSecretsInBackup:            true,
		ForbidSecretsInErrors:            true,
		ForbidPublicTLSTermination:       true,
		ForbidBootstrapRawInContractWire: true,
		ForbidSessionRawInContractWire:   true,
		ForbidCSRFRawInContractWire:      true,
		ForbidForwardedAuthority:         true,
		ForbidMethodOverride:             true,
		ForbidMultipartAndForm:           true,
		ForbidImplicitRedirect:           true,
		RequireDeclaredMethod:            true,
		RequireApplicationJSON:           true,
		RequireNoSniff:                   true,
		RequireNoReferrer:                true,
		RequireFrameDeny:                 true,
		ForbidUnboundedQueue:             true,
	}
}

func (model ThreatModelV1) Validate() error {
	if model != DefaultThreatModelV1() {
		return fmt.Errorf("controlapipolicy: invalid Control API threat model")
	}
	return nil
}

// ValidateListenerConfigurationV1 accepts only the frozen dynamic IPv4
// loopback listener configuration. It performs no network action.
func ValidateListenerConfigurationV1(network, address string) error {
	if network != ControlListenerNetworkV1 || address != ControlListenerAddressV1 {
		return fmt.Errorf(
			"controlapipolicy: listener must be exact %s %s",
			ControlListenerNetworkV1,
			ControlListenerAddressV1,
		)
	}
	return nil
}

// ValidateBoundAuthorityV1 validates the literal authority returned after the
// operating system selects the dynamic port. Hostnames and non-canonical port
// spellings are rejected without DNS resolution.
func ValidateBoundAuthorityV1(authority string) error {
	prefix := "127.0.0.1:"
	if !strings.HasPrefix(authority, prefix) || authority != strings.TrimSpace(authority) {
		return fmt.Errorf("controlapipolicy: bound authority is not literal IPv4 loopback")
	}
	rawPort := authority[len(prefix):]
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != rawPort {
		return fmt.Errorf("controlapipolicy: bound authority has an invalid canonical port")
	}
	return nil
}

func ExactOriginV1(boundAuthority string) (string, error) {
	if err := ValidateBoundAuthorityV1(boundAuthority); err != nil {
		return "", err
	}
	return ControlOriginSchemeV1 + boundAuthority, nil
}

// ValidateAuthorityAndOriginV1 applies byte-exact Host and Origin comparison.
// It does not trim, case-fold, parse forwarded headers, or allow a missing
// Origin on state-bearing requests.
func ValidateAuthorityAndOriginV1(boundAuthority, host, origin string) error {
	wantOrigin, err := ExactOriginV1(boundAuthority)
	if err != nil {
		return err
	}
	if host != boundAuthority {
		return fmt.Errorf("controlapipolicy: Host does not match bound authority")
	}
	if origin != wantOrigin {
		return fmt.Errorf("controlapipolicy: Origin does not match bound origin")
	}
	return nil
}
