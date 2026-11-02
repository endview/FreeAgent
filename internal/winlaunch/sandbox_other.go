//go:build !windows && !linux

package winlaunch

import "context"

func SandboxSupported() bool                               { return false }
func SandboxReason(context.Context) error                  { return ErrUnsupported }
func startSandbox(context.Context, Spec) (*Process, error) { return nil, ErrUnsupported }
