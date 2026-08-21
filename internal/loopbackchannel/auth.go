package loopbackchannel

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const maximumResolvedCredentialBytes = 4096

var errCredentialResolution = errors.New("loopbackchannel: secret resolution failed")

var (
	ErrAuthentication = errors.New("loopbackchannel: authentication failed")
	ErrSecretResolve  = errCredentialResolution
)

// SecretResolver is injected by the trusted composition boundary. Adapter
// construction stores only the reference; the value is resolved for one
// DecodeInbound or ExecutePrepared call and is never retained or logged.
type SecretResolver interface {
	ResolveSecret(context.Context, string) ([]byte, error)
}

func resolveSecret(
	ctx context.Context,
	resolver SecretResolver,
	secretRef string,
) ([]byte, error) {
	if ctx == nil || isNilDependency(resolver) {
		return nil, ErrSecretResolve
	}
	resolved, err := resolver.ResolveSecret(ctx, secretRef)
	if err != nil {
		// Resolver diagnostics are deliberately not wrapped: an implementation
		// may include the resolved value in its error text.
		return nil, ErrSecretResolve
	}
	secret := resolved
	if len(secret) == 0 || len(secret) > maximumResolvedCredentialBytes {
		return nil, ErrSecretResolve
	}
	owned := append([]byte(nil), secret...)
	for _, character := range owned {
		// Authorization bearer credentials must be visible ASCII without
		// whitespace or control bytes. This also prevents header injection.
		if character < 0x21 || character > 0x7e {
			clear(owned)
			return nil, ErrSecretResolve
		}
	}
	return owned, nil
}

func authenticateBearer(headerValues []string, secret []byte) error {
	if len(headerValues) != 1 || len(secret) == 0 {
		return ErrAuthentication
	}
	const prefix = "Bearer "
	provided := headerValues[0]
	if !strings.HasPrefix(provided, prefix) {
		return ErrAuthentication
	}
	candidate := []byte(provided[len(prefix):])
	if len(candidate) != len(secret) || subtle.ConstantTimeCompare(candidate, secret) != 1 {
		return ErrAuthentication
	}
	return nil
}

func authorizationHeader(secret []byte) (string, error) {
	if len(secret) == 0 || len(secret) > maximumResolvedCredentialBytes {
		return "", ErrSecretResolve
	}
	for _, character := range secret {
		if character < 0x21 || character > 0x7e {
			return "", fmt.Errorf("%w: credential cannot be encoded safely", ErrSecretResolve)
		}
	}
	return "Bearer " + string(secret), nil
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}
