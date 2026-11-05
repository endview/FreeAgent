package harness

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The real PG transaction commits. Only its client acknowledgement is lost.
// This checks the uncertain boundary without emulating PostgreSQL storage.
type lostCommitConnector struct {
	driver.Connector
	lose atomic.Bool
}

func (c *lostCommitConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &lostCommitConn{Conn: conn, owner: c}, nil
}

type lostCommitConn struct {
	driver.Conn
	owner *lostCommitConnector
}

func (c *lostCommitConn) Begin() (driver.Tx, error) {
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return lostCommitTx{Tx: tx, owner: c.owner}, nil
}
func (c *lostCommitConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return lostCommitTx{Tx: tx, owner: c.owner}, nil
}

type lostCommitTx struct {
	driver.Tx
	owner *lostCommitConnector
}

func (tx lostCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.owner.lose.CompareAndSwap(true, false) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func TestPostgresAttachmentCommitLostAckRetainsImmutableFile(t *testing.T) {
	tasks, chat, data, _ := attachmentFixture(t, nil, EchoModel{})
	store := tasks.core.Store
	c, err := postgres.Read(filepath.Join(data, postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	var generation string
	if err := store.db.QueryRowContext(t.Context(), `SELECT document->>'generation' FROM fa.meta WHERE key='core/owner'`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig("host=127.0.0.1 sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	config.Host, config.Port, config.User, config.Password, config.Database = c.Host, uint16(c.Port), c.User, c.Password, c.Database
	config.RuntimeParams["freeagent.generation"] = generation
	connector := &lostCommitConnector{Connector: stdlib.GetConnector(*config)}
	connector.lose.Store(true)
	previous := store.db
	defer previous.Close()
	store.db = &pgDatabase{DB: sql.OpenDB(connector), lost: &store.pgOwner.lost}
	ref, err := tasks.UploadAttachmentRequest(t.Context(), chat, "receipt.md", "commit-lost", strings.NewReader("immutable committed content"))
	if !errors.Is(err, io.ErrUnexpectedEOF) || ref.ID == "" {
		t.Fatal("commit uncertainty was hidden", ref, err)
	}
	if _, err := os.Stat(filepath.Join(data, "attachments", ref.ID)); err != nil {
		t.Fatal("committed blob was deleted after a lost acknowledgement", err)
	}
	if _, err := os.Stat(store.attachmentCommitPath(ref.ID)); err != nil {
		t.Fatal("transaction reconciliation marker missing", err)
	}
	if err := store.reconcileAttachmentCommits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.attachmentCommitPath(ref.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed transaction marker remained", err)
	}
	again, err := tasks.UploadAttachmentRequest(t.Context(), chat, "receipt.md", "commit-lost", strings.NewReader("immutable committed content"))
	if err != nil || again.ID != ref.ID {
		t.Fatal("request retry published another attachment", again, err)
	}
	_, body, err := tasks.Attachment(t.Context(), chat, ref.ID)
	if err != nil || string(body) != "immutable committed content" {
		t.Fatal("committed attachment unavailable", err)
	}
}
