// Package publicweb implements the optional single-administrator entrance.
// Execution, permissions and persistent product settings remain in Harness.
package publicweb

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/endview/freeagent/internal/fileops"
	"github.com/endview/freeagent/internal/harness"
	"golang.org/x/crypto/argon2"
)

const credentialName = "public-admin.json"

var ErrCredential = errors.New("public Web: administrator credential is missing or invalid; use the local public password command")
var credentialFiles sync.Mutex

type credential struct {
	Version    int    `json:"version"`
	Memory     uint32 `json:"memory_kib"`
	Time       uint32 `json:"iterations"`
	Threads    uint8  `json:"parallelism"`
	Salt       []byte `json:"salt"`
	Hash       []byte `json:"hash"`
	Generation string `json:"generation"`
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func tokenHash(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func loadCredential(data string) (credential, error) {
	credentialFiles.Lock()
	defer credentialFiles.Unlock()
	file, err := harness.OpenUserAttachmentFile(filepath.Join(data, credentialName))
	if err != nil {
		return credential{}, ErrCredential
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return credential{}, ErrCredential
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 {
		return credential{}, ErrCredential
	}
	var c credential
	if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Memory != 64*1024 || c.Time != 3 || c.Threads != 2 || len(c.Salt) != 16 || len(c.Hash) != 32 || len(c.Generation) != 64 {
		return credential{}, ErrCredential
	}
	return c, nil
}

func (c credential) verify(password []byte) bool {
	if len(password) < 12 || len(password) > 1024 {
		return false
	}
	hash := argon2.IDKey(password, c.Salt, c.Time, c.Memory, c.Threads, 32)
	defer clear(hash)
	return subtle.ConstantTimeCompare(hash, c.Hash) == 1
}

// SetPassword accepts only direct secure input. It returns no hash or secret.
func SetPassword(data string, password []byte) error {
	if len(password) < 12 || len(password) > 1024 || strings.ContainsAny(string(password), "\x00\r\n") {
		return errors.New("public Web: password must contain 12–1024 bytes without line breaks")
	}
	c := credential{Version: 1, Memory: 64 * 1024, Time: 3, Threads: 2, Salt: make([]byte, 16), Generation: randomToken()}
	if _, err := rand.Read(c.Salt); err != nil {
		return err
	}
	c.Hash = argon2.IDKey(password, c.Salt, c.Time, c.Memory, c.Threads, 32)
	defer clear(c.Hash)
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	defer clear(raw)
	credentialFiles.Lock()
	defer credentialFiles.Unlock()
	path := filepath.Join(data, credentialName)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return ErrCredential
		}
		f, err := harness.OpenUserAttachmentFile(path)
		if err != nil {
			return ErrCredential
		}
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(data, ".public-admin-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return fileops.Rename(temporary, path)
}

func CredentialReady(data string) bool { _, err := loadCredential(data); return err == nil }
