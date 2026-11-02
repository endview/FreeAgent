//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/endview/freeagent/internal/apppaths"
)

func guardCLIIdentity(args []string) error {
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help", "--version", "backup-verify":
		return nil
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return nil
		}
	}
	data := defaultDataDir()
	for i, arg := range args {
		if (arg == "--data-dir" || arg == "-data-dir") && i+1 < len(args) {
			data = args[i+1]
		}
		if strings.HasPrefix(arg, "--data-dir=") || strings.HasPrefix(arg, "-data-dir=") {
			_, data, _ = strings.Cut(arg, "=")
		}
	}
	if data == "" {
		return nil
	} // the command reports the explicit path error
	if err := checkOwnedPath(data); err != nil {
		return err
	}
	if path, err := apppaths.KeysFile(); err == nil {
		return checkOwnedPath(path)
	}
	return nil
}

func checkOwnedPath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, err := os.Stat(absolute)
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || int(stat.Uid) != os.Geteuid() {
				return fmt.Errorf("FreeAgent data belongs to another OS user; use the installed freeagent management command (run as the service user)")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return err
		}
		absolute = parent
	}
}

func validateServiceMode() error {
	if os.Geteuid() == 0 {
		return errors.New("web --service refuses root; install/run as the dedicated FreeAgent service user")
	}
	return nil
}
