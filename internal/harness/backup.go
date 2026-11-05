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
	"strings"
)

const BackupFormat = "freeagent-postgres-backup-v1"

type backupManifest struct {
	Attachments  map[string]string `json:"attachments,omitempty"`
	Format       string            `json:"format"`
	IdentityHash string            `json:"identity_sha256"`
	RefsHash     string            `json:"attachment_refs_sha256"`
	StateHash    string            `json:"state_sha256"`
	ConfigHash   string            `json:"config_sha256"`
	Instructions map[string]string `json:"instructions,omitempty"`
}

func VerifyBackup(ctx context.Context, source string) error {
	source, err := canonicalDir(source)
	if err != nil {
		return err
	}
	for _, name := range []string{"state.pgdump", "config.json", "backup.json"} {
		if err := verifyRegularFile(filepath.Join(source, name)); err != nil {
			return fmt.Errorf("harness: backup %s: %w", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(source, "backup.json"))
	if err != nil || len(raw) > 4<<20 {
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
		case "state.pgdump", "config.json", "backup.json", "identity.json", "attachment-refs.json", "instructions", "attachments":
		default:
			return errors.New("harness: unexpected backup file")
		}
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
	stateHash, err := hashFile(filepath.Join(directory, "state.pgdump"))
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
	attachments, err := attachmentManifest(context.Background(), directory)
	if err != nil {
		return backupManifest{}, err
	}
	identityHash, err := hashFile(filepath.Join(directory, "identity.json"))
	if err != nil {
		return backupManifest{}, err
	}
	refsHash, err := hashFile(filepath.Join(directory, "attachment-refs.json"))
	if err != nil {
		return backupManifest{}, err
	}
	return backupManifest{IdentityHash: identityHash, RefsHash: refsHash, Format: BackupFormat, StateHash: stateHash, ConfigHash: configHash, Instructions: instructions, Attachments: attachments}, nil
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

func removeStaging(directory string) error {
	// Only our exact MkdirTemp staging directory is eligible. Never remove a
	// managed cluster whose postmaster did not finish stopping.
	base := filepath.Base(directory)
	if !filepath.IsAbs(directory) || !(strings.HasPrefix(base, ".harness-backup-") || strings.HasPrefix(base, ".harness-restore-")) {
		return errors.New("harness: refusing unexpected staging cleanup path")
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already published
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("harness: unsafe staging cleanup directory"), err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "postgresql", "postmaster.pid")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("harness: PostgreSQL may still be running; staging retained at %s", directory)
	}
	return os.RemoveAll(directory)
}
