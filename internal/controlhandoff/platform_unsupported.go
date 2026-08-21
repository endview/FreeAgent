//go:build !windows && !linux

package controlhandoff

type platformFileIdentityV1 struct{}

func verifyNonElevatedPlatformV1() error        { return ErrElevatedProcess }
func validatePlatformPathV1(string, bool) error { return ErrInvalidInput }
func ensurePrivateRuntimeDirectoryPlatformV1(string) (bool, error) {
	return false, ErrUnsafeParent
}
func verifyPrivateDirectoryPlatformV1(string) error { return ErrUnsafeParent }
func writeExclusivePrivateFilePlatformV1(string, []byte) (platformFileIdentityV1, error) {
	return platformFileIdentityV1{}, ErrSecurityVerification
}
func removeExactPrivateFilePlatformV1(string, platformFileIdentityV1) error {
	return ErrSecurityVerification
}
func removeEmptyDirectoryPlatformV1(string) {}
