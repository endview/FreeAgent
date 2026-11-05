package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseOwnerLossStopsAdmission(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	c, err := postgres.Read(filepath.Join(ui.data, postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv(postgres.ServiceFileEnv)
	if c.Managed {
		path = filepath.Join(ui.data, "postgres-maintenance.json")
	}
	admin, err := postgres.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	admin.Database = c.Database
	db, err := postgres.SQL(admin, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `SELECT pg_terminate_backend((document->>'pid')::int) FROM fa.meta WHERE key='core/owner'`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ui.server.StopRequested():
	case <-time.After(4 * time.Second):
		t.Fatal("Owner loss did not ask the launcher to stop")
	}
	if code, _ := ui.request(t, "POST", "/api/chats", map[string]string{"title": "must not start"}); code != 503 {
		t.Fatal("accepted work after ownership loss", code)
	}
}
