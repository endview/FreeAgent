// Package apppaths locates FreeAgent's data for portable packages and normal
// per-user installs. It is a leaf package: only the standard library and x/sys.
package apppaths

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DataFormat names the only data directory format this build reads.
const DataFormat = "Harness-v5"

// KeysFileEnv overrides the key file location (tests and troubleshooting).
const KeysFileEnv = "FREEAGENT_KEYS_FILE"

// ErrManifest means the executable sits in a portable package whose manifest
// cannot be used. Callers must not fall back to per-user data silently.
var ErrManifest = errors.New("apppaths: invalid portable package manifest")

const maxManifestBytes = 1 << 20

type location struct {
	root     string
	portable bool
}

func locate() (location, error) {
	executable, err := os.Executable()
	if err != nil {
		return location{}, fmt.Errorf("apppaths: locate executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return location{}, fmt.Errorf("apppaths: resolve executable: %w", err)
	}
	root, portable, err := portableRoot(filepath.Dir(executable))
	return location{root: root, portable: portable}, err
}

// portableRoot reports the package root when exeDir, or its parent when exeDir
// is named bin, contains manifest.json. Without a manifest it is not portable.
func portableRoot(exeDir string) (string, bool, error) {
	candidates := []string{exeDir}
	if strings.EqualFold(filepath.Base(exeDir), "bin") {
		candidates = append(candidates, filepath.Dir(exeDir))
	}
	for _, directory := range candidates {
		path := filepath.Join(directory, "manifest.json")
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("%w: %s: %v", ErrManifest, path, err)
		}
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("%w: %s is not a file", ErrManifest, path)
		}
		if err := checkManifest(path); err != nil {
			return "", false, err
		}
		return directory, true, nil
	}
	return "", false, nil
}

// PackageRoot reports the portable package root of the running executable.
// portable is false for a normal per-user install.
func PackageRoot() (root string, portable bool, err error) {
	where, err := locate()
	return where.root, where.portable, err
}

// PackageRootOf reports the portable package root for an executable directory.
func PackageRootOf(exeDir string) (root string, portable bool, err error) {
	return portableRoot(exeDir)
}

func checkManifest(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrManifest, path, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil || len(raw) > maxManifestBytes {
		return fmt.Errorf("%w: %s cannot be read", ErrManifest, path)
	}
	var manifest struct {
		DataFormat *string `json:"data_format"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("%w: %s is not valid JSON", ErrManifest, path)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%w: %s must contain one JSON object", ErrManifest, path)
	}
	if manifest.DataFormat == nil || *manifest.DataFormat != DataFormat {
		return fmt.Errorf("%w: %s data_format must be %s; extract a current package into a new folder", ErrManifest, path, DataFormat)
	}
	return nil
}

// DataDir is the default Harness data directory. --data-dir overrides only it.
func DataDir() (string, error) {
	return choose(func(root string) string { return filepath.Join(root, "data", DataFormat) },
		configDir, DataFormat)
}

// KeysFile is shared by every data directory of the same user or package.
func KeysFile() (string, error) {
	if value := os.Getenv(KeysFileEnv); value != "" {
		path, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("apppaths: %s: %w", KeysFileEnv, err)
		}
		return path, nil
	}
	return choose(func(root string) string { return filepath.Join(root, "data", "keys.json") },
		configDir, "keys.json")
}

func DesktopState() (string, error) {
	return choose(func(root string) string { return filepath.Join(root, "data", "desktop.json") },
		configDir, "desktop.json")
}

func WebView2Dir() (string, error) {
	return choose(func(root string) string { return filepath.Join(root, "data", "webview2") },
		cacheDir, "webview2")
}

func LogDir() (string, error) {
	return choose(func(root string) string { return filepath.Join(root, "data") }, cacheDir, "")
}

// DefaultWorkspace is only a suggestion for first-run initialization.
func DefaultWorkspace() (string, error) {
	where, err := locate()
	if err != nil {
		return "", err
	}
	if where.portable {
		return filepath.Join(where.root, "workspace"), nil
	}
	base, err := workspaceBase()
	if err != nil {
		return "", fmt.Errorf("apppaths: default workspace: %w", err)
	}
	return filepath.Join(base, "FreeAgent"), nil
}

func choose(portable func(string) string, base func() (string, error), name string) (string, error) {
	where, err := locate()
	if err != nil {
		return "", err
	}
	if where.portable {
		return portable(where.root), nil
	}
	directory, err := base()
	if err != nil {
		return "", fmt.Errorf("apppaths: user directory: %w", err)
	}
	if name == "" {
		return filepath.Join(directory, "FreeAgent"), nil
	}
	return filepath.Join(directory, "FreeAgent", name), nil
}

func configDir() (string, error) { return os.UserConfigDir() }

func cacheDir() (string, error) { return os.UserCacheDir() }
