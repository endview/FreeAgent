package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"time"
)

type queryCancelKey struct{}
type transactionCancelKey struct{}

// database/sql retains each context while Scan/Close completes. Its finite
// deadline reclaims the timer; a driver trace must not cancel a successful Scan.
func QueryContext(ctx context.Context) context.Context {
	limited, cancel := context.WithTimeout(ctx, 20*time.Second)
	return context.WithValue(limited, queryCancelKey{}, cancel)
}

// The finite transaction context stays alive until Commit/Rollback or timeout.
func TransactionContext(ctx context.Context) context.Context {
	limited, cancel := context.WithTimeout(ctx, 20*time.Second)
	return context.WithValue(limited, transactionCancelKey{}, cancel)
}

type queryTrace struct{}
type queryTraceKey struct{}

func (queryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryTraceKey{}, data.SQL)
}
func (queryTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {

	if data.Err == nil || os.Getenv("FREEAGENT_PG_QUERY_ERRORS") == "" {
		return
	}
	query, _ := ctx.Value(queryTraceKey{}).(string)
	if strings.Contains(strings.ToUpper(query), "PASSWORD") {
		query = "[credential maintenance]"
	}
	// Explicit local diagnosis only. Bound values are never logged.
	f, err := os.OpenFile(os.Getenv("FREEAGENT_PG_QUERY_ERRORS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, query, "\n", data.Err)
}
