package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// New raw writers must be reviewed for type, audience, adoption and receipt
// semantics. Runtime type rejection alone cannot catch a newly bypassed writer.
func TestConversationMessageWritersHaveProjectionRules(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	known := map[string]int{"store.go": 1, "channel_messages.go": 1, "input_queue.go": 1, "spaces.go": 1, "conversation_branch.go": 1}
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		count := strings.Count(string(body), "INSERT INTO fa.messages")
		if count != known[file.Name()] {
			t.Errorf("%s has %d direct message writers, registered %d; update source rules and regressions before adding one", file.Name(), count, known[file.Name()])
		}
		delete(known, file.Name())
	}
	if len(known) != 0 {
		t.Fatal("registered writer no longer exists", known)
	}
}
