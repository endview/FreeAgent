//go:build windows

package moduleartifactstore

import (
	"fmt"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if err := setWindowsProcessTokenOwnerToCurrentUserV1(); err != nil {
		fmt.Fprintf(os.Stderr, "set Windows test object owner: %v\n", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

type windowsTokenOwnerV1 struct {
	Owner *windows.SID
}

func setWindowsProcessTokenOwnerToCurrentUserV1() error {
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT,
		&token,
	); err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer func() {
		_ = token.Close()
	}()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fmt.Errorf("current user SID: %v", err)
	}
	owner := windowsTokenOwnerV1{Owner: user.User.Sid}
	if err := windows.SetTokenInformation(
		token,
		windows.TokenOwner,
		(*byte)(unsafe.Pointer(&owner)),
		uint32(unsafe.Sizeof(owner)),
	); err != nil {
		return fmt.Errorf("set default object owner: %w", err)
	}
	return nil
}
