//go:build !windows && !linux && !darwin

package safefiletree

import (
	"errors"
	"io/fs"
	"os"
)

type platformDevice struct{}
type platformIdentity struct{}

type platformNode struct {
	file     *os.File
	info     fs.FileInfo
	kind     Kind
	device   platformDevice
	identity platformIdentity
}

func openPlatformRoot(string, bool) (*platformNode, error) {
	return nil, errors.New("platform is unsupported")
}

func openPlatformChild(*platformNode, string, platformDevice, bool, bool) (*platformNode, error) {
	return nil, errors.New("platform is unsupported")
}

func syncPlatformNode(*platformNode) error {
	return errors.New("platform is unsupported")
}

func verifyPlatformNode(*platformNode, platformDevice) error {
	return errors.New("platform is unsupported")
}

func refreshPlatformNodeMetadata(*platformNode) error {
	return errors.New("platform is unsupported")
}
