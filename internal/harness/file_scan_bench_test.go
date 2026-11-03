package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Keep file counts, contents and scan limits identical when comparing versions.
// These are repeated scans with warm filesystem caches, not cold-disk timings.
func BenchmarkWorkspaceScans(b *testing.B) {
	for _, fixture := range []struct {
		name  string
		files int
		deep  bool
	}{{"small", 20, false}, {"flat_10000", 10000, false}, {"nested_1000", 1000, true}} {
		b.Run(fixture.name, func(b *testing.B) {
			workspace, data := b.TempDir(), b.TempDir()
			for i := range fixture.files {
				directory := workspace
				if fixture.deep {
					directory = filepath.Join(workspace, "src", fmt.Sprintf("package-%02d", i/100), "content")
					if err := os.MkdirAll(directory, 0700); err != nil {
						b.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("file-%05d.txt", i)), []byte("workspace observation fixture\nsecond line\n"), 0600); err != nil {
					b.Fatal(err)
				}
			}
			files, err := NewFileTools(workspace, data)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = files.Close() })
			commands := &CommandTools{Files: files}
			b.Run("observe", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					observed, _ := commands.observe(context.Background())
					if len(observed) == 0 {
						b.Fatal("observation omitted every file")
					}
				}
			})
			b.Run("search_absent", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := files.search(context.Background(), ".", "absent needle"); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
