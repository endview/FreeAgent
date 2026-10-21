package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const BackupFormat = "freeagent-harness-backup-v3"

type backupManifest struct {
	Format       string            `json:"format"`
	StateHash    string            `json:"state_sha256"`
	ConfigHash   string            `json:"config_sha256"`
	Instructions map[string]string `json:"instructions,omitempty"`
}

func (store *Store) Backup(ctx context.Context, target string) (returnErr error) {
	if !store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer store.runMu.Unlock()
	target, err := canonicalDir(target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: backup target must not exist")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".harness-backup-*")
	if err != nil {
		return err
	}
	defer removeStaging(stage)
	statePath := filepath.Join(stage, "state.sqlite")
	// VACUUM INTO captures committed WAL pages in a standalone SQLite snapshot.
	if _, err := store.db.ExecContext(ctx, `VACUUM INTO ?`, statePath); err != nil {
		return fmt.Errorf("harness: SQLite snapshot: %w", err)
	}
	if err := os.Chmod(statePath, 0o600); err != nil {
		return err
	}
	if err := verifyStore(ctx, statePath); err != nil {
		return err
	}
	if err := copyRegularFile(filepath.Join(store.dataDir, "config.json"), filepath.Join(stage, "config.json")); err != nil {
		return err
	}
	config, err := backupConfig(stage)
	if err != nil {
		return err
	}
	if err := copyInstructions(store.dataDir, stage, config.Instructions); err != nil {
		return err
	}
	manifest, err := manifestFor(stage)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(stage, "backup.json"), append(raw, '\n')); err != nil {
		return err
	}
	if err := syncFile(statePath); err != nil {
		return err
	}
	return publishDirectory(stage, target)
}

func Restore(ctx context.Context, source, target string) error {
	source, err := canonicalDir(source)
	if err != nil {
		return err
	}
	target, err = canonicalDir(target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: restore target must be a new directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".harness-restore-*")
	if err != nil {
		return err
	}
	defer removeStaging(stage)
	// Copy into private staging first, then verify that exact copied snapshot.
	for _, name := range []string{"state.sqlite", "config.json", "backup.json"} {
		if err := copyRegularFile(filepath.Join(source, name), filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	config, err := backupConfig(stage)
	if err != nil {
		return err
	}
	if err := copyInstructions(source, stage, config.Instructions); err != nil {
		return err
	}
	if err := VerifyBackup(ctx, stage); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(stage, "backup.json")); err != nil {
		return err
	}
	for _, name := range []string{"instructions", "backups"} {
		if err := os.MkdirAll(filepath.Join(stage, name), 0o700); err != nil {
			return err
		}
	}
	return publishDirectory(stage, target)
}

func VerifyBackup(ctx context.Context, source string) error {
	source, err := canonicalDir(source)
	if err != nil {
		return err
	}
	for _, name := range []string{"state.sqlite", "config.json", "backup.json"} {
		if err := verifyRegularFile(filepath.Join(source, name)); err != nil {
			return fmt.Errorf("harness: backup %s: %w", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(source, "backup.json"))
	if err != nil || len(raw) > 4096 {
		return errors.New("harness: invalid backup manifest")
	}
	var expected backupManifest
	if json.Unmarshal(raw, &expected) != nil || expected.Format != BackupFormat ||
		expected.StateHash == "" || expected.ConfigHash == "" {
		return errors.New("harness: unsupported backup format")
	}
	actual, err := manifestFor(source)
	if err != nil {
		return err
	}
	actualBytes, _ := json.Marshal(actual)
	expectedBytes, _ := json.Marshal(expected)
	if !bytes.Equal(actualBytes, expectedBytes) {
		return errors.New("harness: backup checksum verification failed")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "state.sqlite", "config.json", "backup.json", "instructions":
		default:
			return errors.New("harness: unexpected backup file")
		}
	}
	if err := verifyStore(ctx, filepath.Join(source, "state.sqlite")); err != nil {
		return err
	}
	_, err = backupConfig(source)
	return err
}

func backupConfig(source string) (Config, error) {
	var config Config
	configBytes, err := os.ReadFile(filepath.Join(source, "config.json"))
	if err != nil || len(configBytes) > 1<<20 {
		return Config{}, errors.New("harness: unsupported backup config")
	}
	decoder := json.NewDecoder(bytes.NewReader(configBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || config.Version != ConfigVersion {
		return Config{}, errors.New("harness: unsupported backup config")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("harness: backup config must contain one JSON object")
	}
	if err := config.validate(false); err != nil {
		return Config{}, errors.New("harness: invalid backup config")
	}
	seen := map[string]bool{}
	for _, name := range config.Instructions {
		if !validInstructionName(name) || seen[name] {
			return Config{}, errors.New("harness: invalid backed-up instruction name")
		}
		seen[name] = true
	}
	return config, nil
}

func manifestFor(directory string) (backupManifest, error) {
	stateHash, err := hashFile(filepath.Join(directory, "state.sqlite"))
	if err != nil {
		return backupManifest{}, err
	}
	configHash, err := hashFile(filepath.Join(directory, "config.json"))
	if err != nil {
		return backupManifest{}, err
	}
	config, err := backupConfig(directory)
	if err != nil {
		return backupManifest{}, err
	}
	instructions := map[string]string{}
	if _, err := readInstructions(directory, config.Instructions); err != nil {
		return backupManifest{}, err
	}
	for _, name := range config.Instructions {
		hash, err := hashFile(filepath.Join(directory, "instructions", name))
		if err != nil {
			return backupManifest{}, err
		}
		instructions[name] = hash
	}
	return backupManifest{Format: BackupFormat, StateHash: stateHash, ConfigHash: configHash, Instructions: instructions}, nil
}

func copyInstructions(source, target string, names []string) error {
	if len(names) > 0 {
		if _, err := readInstructions(source, names); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(target, "instructions"), 0o700); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !validInstructionName(name) {
			return errors.New("harness: invalid instruction name")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := copyRegularFile(filepath.Join(source, "instructions", name), filepath.Join(target, "instructions", name)); err != nil {
			return err
		}
	}
	return nil
}

func hashFile(path string) (string, error) {
	if _, err := canonicalDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	if err := verifyRegularFile(path); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := checkRegularHandle(file); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyRegularFile(source, target string) error {
	if err := verifyRegularFile(source); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := checkRegularHandle(in); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func writeNewFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func publishDirectory(stage, target string) error {
	return publishDirectoryNoReplace(stage, target)
}

func removeStaging(directory string) {
	entries, _ := os.ReadDir(filepath.Join(directory, "instructions"))
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(directory, "instructions", entry.Name()))
	}
	_ = os.Remove(filepath.Join(directory, "instructions"))
	_ = os.Remove(filepath.Join(directory, "backups"))
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm", "config.json", "backup.json"} {
		_ = os.Remove(filepath.Join(directory, name))
	}
	_ = os.Remove(directory)
}
