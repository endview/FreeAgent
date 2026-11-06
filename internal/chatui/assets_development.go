//go:build freeagentdev

package chatui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

var developmentAssetOnce sync.Once
var developmentAssetFS fs.FS

// Only source builds with -tags freeagentdev may serve a built frontend from
// disk. The normal same-origin authentication/CSP handlers remain in place.
func developmentAssets(embedded fs.FS) fs.FS {
	developmentAssetOnce.Do(func() {
		root := os.Getenv("FREEAGENT_UI_DEV_ROOT")
		if root == "" {
			developmentAssetFS = embedded
			return
		}
		if !filepath.IsAbs(root) {
			panic("development UI directory must be absolute")
		}
		marker, err := os.ReadFile(filepath.Join(root, "freeagent-development-ui.json"))
		if err != nil || string(marker) != "{\"development_ui\":true}\n" {
			panic("development UI marker missing")
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("development UI links are forbidden")
			}
			return nil
		})
		if err != nil {
			panic("development UI directory is invalid")
		}
		developmentAssetFS = os.DirFS(root)
	})
	return developmentAssetFS
}
