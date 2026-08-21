package currentstore

import (
	"context"
	"database/sql"
)

// readQueryerV1 is the minimum shared surface implemented by *sql.Conn and
// *sql.Tx for semantic closure verification. It lets Overview reuse the
// authoritative verifiers inside its one coherent caller-owned transaction.
type readQueryerV1 interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
