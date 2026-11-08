package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillPublicationRejectsUnsafeRevisionArchive(t *testing.T) {
	for _, mode := range []string{"linked-directory", "changed-history"} {
		t.Run(mode, func(t *testing.T) {
			core, _, _ := testHarness(t, EchoModel{})
			control, err := NewControlService(NewTaskService(core))
			if err != nil {
				t.Fatal(err)
			}
			document := "---\nname: lesson\ndescription: Synthetic reviewed procedure\ntrigger: manual\n---\nOriginal verified method.\n"
			root := filepath.Join(core.Store.DataDir(), "skills")
			target := filepath.Join(root, "lesson", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(document), 0600); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if mode == "linked-directory" {
				if err := os.Symlink(outside, filepath.Join(root, ".revisions")); err != nil {
					t.Skipf("NOT_RUN: test-user symlink creation unavailable: %v", err)
				}
			} else {
				archive := filepath.Join(root, ".revisions", "lesson")
				if err := os.MkdirAll(archive, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(archive, contentHash([]byte(document))+".md"), []byte("unrelated content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			record := controlRecord{Arguments: mustJSON(SkillPublication{ID: "lesson", ExpectedSHA256: contentHash([]byte(document)), Document: document + "New reviewed method.\n"})}
			if err := control.buildSkillPublication(t.Context(), &record); err != nil {
				t.Fatal(err)
			}
			if err := control.applySkillPublication(t.Context(), &record); err == nil {
				t.Fatal("unsafe revision storage was accepted")
			}
			raw, err := os.ReadFile(target)
			if err != nil || string(raw) != document {
				t.Fatal("rejected replacement changed the active document", err)
			}
			files, err := os.ReadDir(outside)
			if err != nil || len(files) != 0 {
				t.Fatal("revision escaped its data root", err)
			}
		})
	}
}
