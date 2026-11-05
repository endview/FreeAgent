package channelplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/endview/freeagent/internal/fileops"
)

func replaceCursor(source, target string) error { return fileops.Rename(source, target) }
func claimConnection(source, application string) (func(), error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, errors.New("channel: private lock directory unavailable")
	}
	dir := filepath.Join(base, "FreeAgent", "channel-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("channel: private lock directory unavailable")
	}
	hash := sha256.Sum256([]byte(source + "\x00" + application))
	return lockConnection(filepath.Join(dir, hex.EncodeToString(hash[:])+".lock"))
}
