package harness

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/endview/freeagent/internal/postgres"
)

//go:embed postgres_schema.sql
var postgresSchema string

// SQL remains ordinary, statically written PostgreSQL. This wrapper only bounds
// pool acquisition/transactions; it never rewrites queries or retries effects.
type pgDatabase struct {
	*sql.DB
	lost *atomic.Bool
}

func databaseContext(ctx context.Context) context.Context {
	// SQL rows/transactions retain the context until Close/Commit. The finite
	// deadline also bounds pool wait for callers without their own deadline.
	return postgres.QueryContext(ctx)
}
func (db *pgDatabase) ready() error {
	if db.lost != nil && db.lost.Load() {
		return ErrOwnerLost
	}
	return nil
}
func (db *pgDatabase) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if err := db.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return db.DB.ExecContext(ctx, q, args...)
}
func (db *pgDatabase) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if err := db.ready(); err != nil {
		return nil, err
	}
	return db.DB.QueryContext(databaseContext(ctx), q, args...)
}
func (db *pgDatabase) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(databaseContext(ctx), q, args...)
}
func (db *pgDatabase) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	if err := db.ready(); err != nil {
		return nil, err
	}
	if options != nil && options.ReadOnly && options.Isolation == sql.LevelDefault {
		copy := *options
		copy.Isolation = sql.LevelRepeatableRead
		options = &copy
	}
	return db.DB.BeginTx(postgres.TransactionContext(ctx), options)
}

var ErrOwnerLost = errors.New("harness: PostgreSQL Owner lock lost; new execution stopped")

type pgOwner struct {
	ctx            context.Context
	cancel         context.CancelFunc
	connection     *sql.Conn
	db             *sql.DB
	releaseRuntime func() error
	stop           chan struct{}
	done           chan struct{}
	lost           atomic.Bool
	once           sync.Once
}

func (o *pgOwner) close() error {
	if o == nil {
		return nil
	}
	o.once.Do(func() { close(o.stop); <-o.done })
	if o.cancel != nil {
		o.cancel()
	}
	return errors.Join(o.connection.Close(), o.db.Close(), o.releaseRuntime())
}
func (store *Store) watchPGOwner(o *pgOwner) {
	defer close(o.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-o.stop:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var valid bool
		err := o.connection.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=1178686216::oid AND objid=1::oid AND granted)`).Scan(&valid)
		cancel()
		if err != nil || !valid {
			o.lost.Store(true)
			o.cancel()
			store.cancelMu.Lock()
			for _, cancel := range store.requestCancels {
				cancel()
			}
			store.cancelMu.Unlock()
			_ = store.db.DB.Close()
			return
		}
	}
}

func initPostgresStore(ctx context.Context, dataDir string) (returnErr error) {
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("harness: initialization requires an empty new data directory")
	}
	owner, err := acquireProcessFileLock(filepath.Join(dataDir, "owner.lock"))
	if err != nil {
		return err
	}
	defer owner.release()
	c, admin, release, err := postgres.Bootstrap(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, admin.Close(), release()) }()
	if err := createSchema(ctx, admin); err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('core/external','{"revision":0}')`); err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('core/id',$1),('format',$2),('schema/version',$3)`, c.CoreID, StoreFormat, fmt.Sprint(storeSchemaVersion)); err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('schema/source',$1)`, contentHash([]byte(postgresSchema))); err != nil {
		return err
	}
	if err := seedLocalSpace(ctx, admin); err != nil {
		return err
	}
	identity, err := schemaIdentity(ctx, admin)
	if err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('schema/identity',$1)`, identity); err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `GRANT USAGE ON SCHEMA fa TO `+c.User+`; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA fa TO `+c.User+`; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA fa TO `+c.User); err != nil {
		return err
	}
	for _, name := range []string{"instructions", "backups"} {
		if err := os.Mkdir(filepath.Join(dataDir, name), 0700); err != nil {
			return err
		}
	}
	return nil
}
func createSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, postgresSchema); err != nil {
		return fmt.Errorf("harness: initialize PostgreSQL schema: %w", err)
	}
	return tx.Commit()
}
func openPostgresStore(ctx context.Context, dataDir string, readOnly bool) (_ *Store, returnErr error) {
	dataDir, err := canonicalDir(dataDir)
	if err != nil {
		return nil, errors.Join(ErrStoreFormat, err)
	}
	if _, err := backupConfig(dataDir); err != nil {
		return nil, errors.Join(ErrStoreFormat, err)
	}
	var fileOwner processFileLock
	if !readOnly {
		fileOwner, err = acquireProcessFileLock(filepath.Join(dataDir, "owner.lock"))
		if err != nil {
			return nil, err
		}
	}
	defer func() {
		if returnErr != nil {
			_ = fileOwner.release()
		}
	}()
	acquire := postgres.Acquire
	if !readOnly {
		acquire = postgres.AcquireOwner
	}
	c, release, err := acquire(ctx, dataDir)
	if err != nil {
		return nil, errors.Join(ErrStoreFormat, err)
	}
	defer func() {
		if returnErr != nil {
			_ = release()
		}
	}()
	db, err := postgres.SQL(c, "", readOnly)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnErr != nil {
			_ = db.Close()
		}
	}()
	if err := verifyPostgres(ctx, db, c.CoreID); err != nil {
		return nil, err
	}
	store := &Store{dataDir: dataDir, db: &pgDatabase{DB: db}, owner: fileOwner, runtimeRelease: release}
	if readOnly {
		return store, nil
	}
	connection, err := db.Conn(databaseContext(ctx))
	if err != nil {
		return nil, err
	}
	var locked bool
	if err := connection.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(1178686216,1)`).Scan(&locked); err != nil || !locked {
		_ = connection.Close()
		return nil, errors.Join(ErrOwnerActive, err)
	}
	var generation string
	if err := connection.QueryRowContext(ctx, `SELECT fa.claim_owner()`).Scan(&generation); err != nil {
		_ = connection.Close()
		return nil, err
	}
	pool, err := postgres.SQL(c, generation, false)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	o := &pgOwner{connection: connection, db: db, releaseRuntime: release, stop: make(chan struct{}), done: make(chan struct{})}
	o.ctx, o.cancel = context.WithCancel(context.Background())
	store.db = &pgDatabase{DB: pool, lost: &o.lost}
	store.pgOwner = o
	store.runtimeRelease = nil
	go store.watchPGOwner(o)
	if err := store.recover(ctx); err != nil {
		_ = pool.Close()
		_ = o.close()
		return nil, err
	}
	return store, nil
}
func verifyPostgres(ctx context.Context, db *sql.DB, coreID string) error {
	var id, format, version, identity string
	for key, target := range map[string]*string{"core/id": &id, "format": &format, "schema/version": &version, "schema/identity": &identity} {
		if err := db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(target); err != nil {
			return errors.Join(ErrStoreFormat, err)
		}
	}
	if id == "" || id != coreID || format != StoreFormat || version != fmt.Sprint(storeSchemaVersion) {
		return ErrStoreFormat
	}
	var source string
	if err := db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='schema/source'`).Scan(&source); err != nil || source != contentHash([]byte(postgresSchema)) {
		return errors.Join(ErrStoreFormat, err)
	}
	actual, err := schemaIdentity(ctx, db)
	if err != nil || identity != actual {
		return errors.Join(ErrStoreFormat, err)
	}
	var extension bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&extension); err != nil || !extension {
		return errors.Join(errors.New("harness: required pg_trgm extension unavailable"), err)
	}
	return nil
}
func schemaIdentity(ctx context.Context, db *sql.DB) (string, error) {
	// Reconstruct the actual catalog, not a client-side version-only promise.
	rows, err := db.QueryContext(ctx, `SELECT definition FROM (
 SELECT 'column:'||c.relname||':'||a.attnum||':'||a.attname||':'||format_type(a.atttypid,a.atttypmod)||':'||a.attnotnull||':'||COALESCE(pg_get_expr(d.adbin,d.adrelid),'') AS definition
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname='fa' AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraint:'||c.relname||':'||pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fa'
 UNION ALL SELECT 'index:'||indexdef FROM pg_indexes WHERE schemaname='fa'
 UNION ALL SELECT 'function:'||pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='fa'
 UNION ALL SELECT 'trigger:'||pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fa' AND NOT t.tgisinternal
 ) definitions ORDER BY definition`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var text string
	var count int
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		text += s + "\n"
		count++
		if len(text) > 2<<20 {
			return "", ErrStoreFormat
		}
	}
	if count < 50 {
		return "", ErrStoreFormat
	}
	return contentHash([]byte(text)), rows.Err()
}
