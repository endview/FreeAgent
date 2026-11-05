package harness

import (
	"context"
	"github.com/endview/freeagent/internal/postgres"
	"path/filepath"
)

type DatabaseStatus struct {
	Backend    string `json:"backend"`
	Version    string `json:"version"`
	Managed    bool   `json:"managed"`
	CoreID     string `json:"core_id"`
	Schema     int    `json:"schema"`
	Bytes      int64  `json:"bytes"`
	PoolLimit  int    `json:"pool_limit"`
	PoolOpen   int    `json:"pool_open"`
	PoolInUse  int    `json:"pool_in_use"`
	PoolWaits  int64  `json:"pool_waits"`
	PoolWaitMS int64  `json:"pool_wait_ms"`
	OwnerLost  bool   `json:"owner_lost"`
}

func (tasks *TaskService) DatabaseStatus(ctx context.Context) (DatabaseStatus, error) {
	store := tasks.core.Store
	c, err := postgres.Read(filepath.Join(store.dataDir, postgres.ConfigFile))
	if err != nil {
		return DatabaseStatus{}, err
	}
	s := DatabaseStatus{Backend: "PostgreSQL", Managed: c.Managed, CoreID: c.CoreID, Schema: storeSchemaVersion}
	if err := store.db.QueryRowContext(ctx, `SELECT current_setting('server_version'),pg_database_size(current_database())`).Scan(&s.Version, &s.Bytes); err != nil {
		return s, err
	}
	stats := store.db.Stats()
	s.PoolLimit, s.PoolOpen, s.PoolInUse, s.PoolWaits, s.PoolWaitMS = stats.MaxOpenConnections, stats.OpenConnections, stats.InUse, stats.WaitCount, stats.WaitDuration.Milliseconds()
	if store.pgOwner != nil {
		s.OwnerLost = store.pgOwner.lost.Load()
	}
	return s, nil
}
