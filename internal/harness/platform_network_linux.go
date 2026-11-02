//go:build linux

package harness

import "errors"

func validatePlatformNetwork(config Config, kind string) error {
	switch kind {
	case ControlSetNetwork, ControlSetExecutionProfile, ControlSetSandbox, ControlSetPermission:
	default:
		return nil
	}
	settings := effectiveExecutionSettings(config)
	if settings.NetworkPolicy.Mode == NetworkInternet && settings.PermissionMode != PermissionFull && settings.SandboxMode != SandboxHostFull {
		return errors.New("Linux sandbox supports network-off only; explicitly select Full for host networking")
	}
	return nil
}
