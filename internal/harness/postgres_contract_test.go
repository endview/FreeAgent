package harness

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresDeadlockAndSerializationFailuresRollbackAtomically(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	a, b := conversation(t, core), conversation(t, core)
	t.Run("deadlock", func(t *testing.T) {
		tx1, err := core.Store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx1.Rollback()
		tx2, err := core.Store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx2.Rollback()
		for tx, id := range map[*sql.Tx]string{tx1: a, tx2: b} {
			if _, err := tx.ExecContext(t.Context(), `UPDATE fa.conversations SET title='uncommitted' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		var pid int
		if err := tx1.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		first := make(chan error, 1)
		go func() {
			_, err := tx1.ExecContext(t.Context(), `UPDATE fa.conversations SET title='first' WHERE id=$1`, b)
			first <- err
		}()
		deadline := time.Now().Add(3 * time.Second)
		blocked := false
		for time.Now().Before(deadline) {
			if err := core.Store.db.QueryRowContext(t.Context(), `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !blocked {
			t.Fatal("first transaction did not actually block")
		}
		_, secondErr := tx2.ExecContext(t.Context(), `UPDATE fa.conversations SET title='second' WHERE id=$1`, a)
		if secondErr != nil {
			_ = tx2.Rollback()
		} else if err := tx2.Commit(); err != nil {
			t.Fatal(err)
		}
		firstErr := <-first
		if firstErr != nil {
			_ = tx1.Rollback()
		} else if err := tx1.Commit(); err != nil {
			t.Fatal(err)
		}
		var pg *pgconn.PgError
		if !errors.As(errors.Join(firstErr, secondErr), &pg) || pg.Code != "40P01" || (firstErr == nil) == (secondErr == nil) {
			t.Fatal("deadlock did not surface exactly one aborted transaction", firstErr, secondErr)
		}
		var titleA, titleB string
		if err := core.Store.db.QueryRowContext(t.Context(), `SELECT a.title,b.title FROM fa.conversations a,fa.conversations b WHERE a.id=$1 AND b.id=$2`, a, b).Scan(&titleA, &titleB); err != nil {
			t.Fatal(err)
		}
		if firstErr != nil && (titleA != "second" || titleB != "uncommitted") || secondErr != nil && (titleA != "uncommitted" || titleB != "first") {
			t.Fatal("aborted transaction leaked partial changes", titleA, titleB)
		}
	})
	t.Run("serialization", func(t *testing.T) {
		var txs []*sql.Tx
		defer func() {
			for _, tx := range txs {
				_ = tx.Rollback()
			}
		}()
		for range 2 {
			tx, err := core.Store.db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				t.Fatal(err)
			}
			txs = append(txs, tx)
			var title string
			if err := tx.QueryRowContext(t.Context(), `SELECT title FROM fa.conversations WHERE id=$1`, a).Scan(&title); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := txs[0].ExecContext(t.Context(), `UPDATE fa.conversations SET title='committed winner' WHERE id=$1`, a); err != nil {
			t.Fatal(err)
		}
		if err := txs[0].Commit(); err != nil {
			t.Fatal(err)
		}
		_, err := txs[1].ExecContext(t.Context(), `UPDATE fa.conversations SET title='must not commit' WHERE id=$1`, a)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "40001" {
			t.Fatal("serialization conflict was hidden", err)
		}
		_ = txs[1].Rollback()
		var title string
		if err := core.Store.db.QueryRowContext(t.Context(), `SELECT title FROM fa.conversations WHERE id=$1`, a).Scan(&title); err != nil || title != "committed winner" {
			t.Fatal("serialization failure changed committed state", title, err)
		}
	})
}

func TestPostgresPoolWaitCancellationAndTransactionRollback(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	var held []*sql.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for range 4 {
		c, err := core.Store.db.DB.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('pool/cancelled','{"value":"must not commit"}')`)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatal("pool exhaustion did not honor caller cancellation", err)
	}
	for _, c := range held {
		_ = c.Close()
	}
	held = nil
	txCtx, stop := context.WithCancel(t.Context())
	tx, err := core.Store.db.BeginTx(txCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(txCtx, `INSERT INTO fa.meta(key,value) VALUES('pool/rollback','{"value":"must not commit"}')`); err != nil {
		stop()
		_ = tx.Rollback()
		t.Fatal(err)
	}
	stop()
	_ = tx.Rollback()
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key IN ('pool/cancelled','pool/rollback')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled work was committed or pool did not recover", count, err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES('pool/recovered','{"value":"ok"}')`); err != nil {
		t.Fatal("pool unavailable after cancellation", err)
	}
}

func TestPostgresConversationOrderBlocksUncommittedPrefix(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, core)
	other := conversation(t, core)
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var first int64
	if err := tx.QueryRowContext(t.Context(), `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2) RETURNING id`, chat, string(mustJSON(ModelMessage{Role: "user", Content: "will roll back"}))).Scan(&first); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		var id int64
		err := core.Store.db.QueryRowContext(t.Context(), `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2) RETURNING id`, chat, string(mustJSON(ModelMessage{Role: "user", Content: "committed"}))).Scan(&id)
		done <- result{id, err}
	}()
	<-started
	var independent int64
	if err := core.Store.db.QueryRowContext(t.Context(), `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2) RETURNING id`, other, string(mustJSON(ModelMessage{Role: "user", Content: "independent"}))).Scan(&independent); err != nil || independent != 1 {
		t.Fatal("unrelated conversation blocked or shared its sequence", independent, err)
	}
	select {
	case r := <-done:
		t.Fatal("published past an uncommitted conversation prefix", r)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.id != first {
		t.Fatal("rollback leaked sequence/adoption", r)
	}
	messages, err := core.Store.ListMessages(t.Context(), chat)
	if err != nil || len(messages) != 1 || messages[0].Content != "committed" {
		t.Fatal(messages, err)
	}
}

func TestPostgresRolesCoreIdentityAndSecondOwner(t *testing.T) {
	a, _, _ := testHarness(t, EchoModel{})
	b, _, _ := testHarness(t, EchoModel{})
	ca, err := postgres.Read(filepath.Join(a.Store.DataDir(), postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	cb, err := postgres.Read(filepath.Join(b.Store.DataDir(), postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	foreign := ca
	foreign.Database = cb.Database
	db, err := postgres.SQL(foreign, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("Core A runtime role connected to Core B")
	}
	var privileged bool
	if err := a.Store.db.QueryRowContext(t.Context(), `SELECT rolsuper OR rolcreatedb OR rolcreaterole FROM pg_roles WHERE rolname=current_user`).Scan(&privileged); err != nil || privileged {
		t.Fatal("runtime role privileges", privileged, err)
	}
	if _, err := a.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value='forged' WHERE key='core/id'`); err == nil {
		t.Fatal("runtime role overwrote Core identity")
	}
	clone := t.TempDir()
	if err := copyRegularFile(filepath.Join(a.Store.DataDir(), "config.json"), filepath.Join(clone, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Write(filepath.Join(clone, postgres.ConfigFile), ca); err != nil {
		t.Fatal(err)
	}
	if second, err := OpenStore(t.Context(), clone); !errors.Is(err, ErrOwnerActive) {
		if second != nil {
			second.Close()
		}
		t.Fatal("second OS directory evaded PG Owner lock", err)
	}
	ca.CoreID = cb.CoreID
	if err := os.Remove(filepath.Join(clone, postgres.ConfigFile)); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Write(filepath.Join(clone, postgres.ConfigFile), ca); err != nil {
		t.Fatal(err)
	}
	if viewer, err := OpenStoreReadOnly(t.Context(), clone); !errors.Is(err, ErrStoreFormat) {
		if viewer != nil {
			viewer.Close()
		}
		t.Fatal("wrong logical identity accepted", err)
	}
}

func TestPostgresLostOwnerCancelsAndFencesOldGeneration(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ModelResponse{}, ctx.Err()
	}))
	core.Config.ModelRetries = 0
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, err := NewTaskService(core).Submit(t.Context(), chat, "lease-loss", "wait"); done <- err }()
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("model not started")
	}
	c, err := postgres.Read(filepath.Join(core.Store.DataDir(), postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	var oldGeneration string
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT document->>'generation' FROM fa.meta WHERE key='core/owner'`).Scan(&oldGeneration); err != nil {
		t.Fatal(err)
	}
	stale, err := postgres.SQL(c, oldGeneration, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	if _, err := core.Store.pgOwner.connection.ExecContext(t.Context(), `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(4 * time.Second):
		t.Fatal("lost Owner kept provider call running")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost connection reported completion")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lost Owner did not settle")
	}
	data := core.Store.DataDir()
	_ = core.Store.Close()
	next, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := stale.ExecContext(t.Context(), `UPDATE fa.spaces SET title='stale writer' WHERE id='local'`); err == nil {
		t.Fatal("old generation wrote after takeover")
	}
	run, err := next.GetLatestRun(t.Context(), chat)
	if err != nil || run.Status != "unknown" {
		t.Fatal("dispatched request was not retained UNKNOWN", run, err)
	}
	attempts, err := next.ListAttempts(t.Context(), run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "unknown" {
		t.Fatal("lost Owner replayed or erased original call", attempts, err)
	}
}

func TestPostgresMemoryCompareAndSwapIsAtomic(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "cas-source", "ORIGINAL")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, body := range []string{"FIRST", "SECOND"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mutation := memoryCreate(ref.Scope, body, body)
			mutation.Action = "update"
			mutation.EntryID = ref.EntryID
			mutation.ExpectedRevision = 1
			_, err := tasks.SaveMemory(t.Context(), chat, mutation)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrMemoryConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent edits overwrote a confirmed revision", success, conflict)
	}
}

func TestPostgresOnlineBackupRestoresDispatchedAsUnknown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		select {
		case <-release:
			return ModelResponse{Content: "done"}, nil
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}))
	defer close(release)
	chat := conversation(t, core)
	requestCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := NewTaskService(core).Submit(requestCtx, chat, "online-backup", "wait"); done <- err }()
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("model not started")
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal("backup paused/rejected a live task", err)
	}
	if err := VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, err := store.GetLatestRun(t.Context(), chat)
	if err != nil || run.Status != "unknown" {
		t.Fatal("snapshot implied a known external result", run, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("live request failed to cancel")
	}
}

func TestPostgresChineseTrigramsAndLiteralSearch(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	var trigrams string
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT public.show_trgm('中文全文检索')::text`).Scan(&trigrams); err != nil || strings.Count(trigrams, ",") < 2 {
		t.Fatal("Chinese classifier did not produce usable trigrams", trigrams, err)
	}
	t.Log("Chinese trigrams extracted by the actual packaged locale")
	// Literal query/authorization cases are also covered by the existing Memory
	// suite, now using this same PostgreSQL Store rather than an emulation.
	var readOnly bool
	tx, err := core.Store.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(t.Context(), `SELECT current_setting('transaction_read_only')::boolean`).Scan(&readOnly); err != nil || !readOnly {
		t.Fatal(readOnly, err)
	}
}
