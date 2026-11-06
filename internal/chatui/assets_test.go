package chatui

import (
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsOnlyWoff2Fonts(t *testing.T) {
	woff2 := 0
	err := fs.WalkDir(Assets(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".woff", ".ttf":
			t.Errorf("embedded assets must only ship woff2 fonts, found %s", name)
		case ".woff2":
			woff2++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if woff2 == 0 {
		t.Fatal("no woff2 formula fonts are embedded")
	}
}

func TestReleaseAssetsExcludeDevelopmentContent(t *testing.T) {
	err := fs.WalkDir(Assets(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".map") || strings.Contains(name, "Gallery-") || strings.Contains(name, "freeagent-development-ui") {
			t.Errorf("development asset shipped: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedChatAssetsRespectSameOriginCSP(t *testing.T) {
	assets := Assets()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil || !strings.Contains(string(index), `href="/favicon.svg"`) {
		t.Fatalf("production page is missing its local favicon: %v", err)
	}
	styles, err := fs.Glob(assets, "assets/*.css")
	if err != nil || len(styles) == 0 {
		t.Fatalf("expected bundled stylesheets: %v %v", styles, err)
	}
	for _, name := range styles {
		css, err := fs.ReadFile(assets, name)
		if err != nil || regexp.MustCompile(`(?i)url\(\s*['"]?(?:data:|https?:|//)`).Match(css) {
			t.Fatalf("bundled styles must keep fonts and resources same-origin (%s): %v", name, err)
		}
	}
	fonts, err := fs.Glob(assets, "assets/*.woff2")
	if err != nil || len(fonts) == 0 {
		t.Fatalf("no bundled formula fonts: %v", err)
	}
	ui := setupUI(t, nil)
	for _, path := range []string{"/favicon.svg", "/" + styles[0], "/" + fonts[0]} {
		code, body := ui.request(t, http.MethodGet, path, nil)
		if code != http.StatusOK || len(body) == 0 {
			t.Fatalf("embedded resource %s returned %d, bytes=%d", path, code, len(body))
		}
	}
}
