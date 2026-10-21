//go:build !windows

package apppaths

import "os"

func workspaceBase() (string, error) { return os.UserHomeDir() }
