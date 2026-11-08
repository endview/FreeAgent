package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"strings"
)

// Provisioning already created the declared schema with its restricted owner.
// Restore its objects directly from the paired archive, without starting the
// extension or knowing its business tables. Reject objects outside that schema.
func extensionRestoreList(ctx context.Context, c postgres.Connection, archive string) ([]byte, error) {
	command, err := postgres.Command(ctx, c, "pg_restore", "--list", archive)
	if err != nil {
		return nil, err
	}
	raw, err := command.Output()
	if err != nil || len(raw) > 2<<20 {
		return nil, errors.New("harness: invalid extension archive table of contents")
	}
	var result strings.Builder
	objects := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, ";") || strings.TrimSpace(line) == "" {
			continue
		}
		_, entry, ok := strings.Cut(line, "; ")
		fields := strings.Fields(entry)
		if !ok || len(fields) < 4 {
			return nil, ErrStoreFormat
		}
		description := strings.Join(fields[2:], " ")
		if strings.HasPrefix(description, "SCHEMA - "+c.Schema+" ") {
			continue
		}
		allowed := false
		for _, kind := range []string{"TABLE DATA", "SEQUENCE SET", "SEQUENCE OWNED BY", "FK CONSTRAINT", "TABLE", "SEQUENCE", "INDEX", "CONSTRAINT", "DEFAULT", "TYPE", "FUNCTION", "TRIGGER"} {
			if strings.HasPrefix(description, kind+" "+c.Schema+" ") {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, errors.New("harness: extension archive contains an unsupported object or another schema")
		}
		result.WriteString(line + "\n")
		objects++
	}
	if objects == 0 {
		return nil, ErrStoreFormat
	}
	return []byte(result.String()), nil
}
