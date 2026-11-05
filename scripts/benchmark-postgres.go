//go:build ignore

// Run only against scripts/test-postgres.py's isolated service:
// python scripts/test-postgres.py --runtime <postgresql> -- go run scripts/benchmark-postgres.go
// Seeds 100k Runs, 400k messages and 300k Attempts (~1.8 GiB); Echo has no model delay.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func sample(ctx context.Context, tasks *harness.TaskService, chat string, n int) map[string]any {
	values := make([]float64, 0, n)
	for i := 0; i < n+10; i++ {
		started := time.Now()
		r, err := tasks.Submit(ctx, chat, fmt.Sprintf("bench-%d", time.Now().UnixNano()), "Echo one short benchmark sentence.")
		must(err)
		if r.Run.Status != "completed" {
			panic(r.Run.Status)
		}
		if i >= 10 {
			values = append(values, float64(time.Since(started).Microseconds())/1000)
		}
	}
	sort.Float64s(values)
	return map[string]any{"samples": n, "p50_ms": values[n/2], "p95_ms": values[(n-1)*95/100], "min_ms": values[0], "max_ms": values[n-1]}
}
func main() {
	if os.Getenv(postgres.ServiceFileEnv) == "" {
		panic("use scripts/test-postgres.py with an isolated PostgreSQL service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "freeagent-pg8-benchmark-")
	must(err)
	defer os.RemoveAll(root)
	data, work := filepath.Join(root, "data"), filepath.Join(root, "workspace")
	must(os.Mkdir(work, 0700))
	must(harness.InitStore(ctx, data))
	cfg := harness.DefaultConfig(work)
	cfg.Models = map[string]harness.ModelConfig{"echo": {Provider: "echo"}}
	cfg.DefaultModel = "echo"
	cfg.TaskSummaryBytes = 0
	must(harness.WriteConfig(data, cfg))
	started := time.Now()
	tasks, closeAll, err := harness.OpenTasks(ctx, data, harness.Adapters{})
	must(err)
	emptyStartup := time.Since(started).Milliseconds()
	chat, err := tasks.NewConversation(ctx, "benchmark")
	must(err)
	report := map[string]any{"workload": "actual single-Core TaskService; synthetic Echo, no Provider delay", "baseline": sample(ctx, tasks, chat.ID, 80), "empty_start_ms": emptyStartup}
	c, err := postgres.Read(filepath.Join(data, postgres.ConfigFile))
	must(err)
	admin, err := postgres.Read(os.Getenv(postgres.ServiceFileEnv))
	must(err)
	admin.Database = c.Database
	db, err := postgres.SQL(admin, "", false)
	must(err)
	defer db.Close()
	exec := func(q string, args ...any) { _, err := db.ExecContext(ctx, q, args...); must(err) }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec(`INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) SELECT md5('historical-chat-'||g),'synthetic history',$1,'local',$2,$2 FROM generate_series(0,999) g`, work, now)
	config, _ := json.Marshal(cfg)
	for offset := 0; offset < 100000; offset += 1000 {
		exec(`INSERT INTO fa.runs(id,conversation_id,request_key,prompt,config_json,status,stop_reason,steps,elapsed_ms,output,created_at,updated_at) SELECT md5('historical-run-'||g),md5('historical-chat-'||(g%1000)),g::text,repeat('p',512),$3,'completed','',1,1,repeat('o',512),$4,$4 FROM generate_series($1::int,$2::int) g`, offset, offset+999, string(config), now)
		exec(`INSERT INTO fa.messages(conversation_id,run_id,body_json) SELECT md5('historical-chat-'||(g%1000)),md5('historical-run-'||g),jsonb_build_object('role',CASE WHEN m%2=0 THEN 'assistant' ELSE 'user' END,'content',repeat('m',512)) FROM generate_series($1::int,$2::int) g CROSS JOIN generate_series(1,4) m`, offset, offset+999)
		exec(`INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) SELECT md5('historical-attempt-'||g||'-'||a),md5('historical-run-'||g),'model','','finished',jsonb_build_object('content',repeat('q',512))::text,jsonb_build_object('content',repeat('r',512))::text FROM generate_series($1::int,$2::int) g CROSS JOIN generate_series(1,3) a`, offset, offset+999)
	}
	exec(`ANALYZE fa.runs; ANALYZE fa.messages; ANALYZE fa.attempts; ANALYZE fa.conversations`)
	report["history_runs"] = 100000
	report["history_messages"] = 400000
	report["history_attempts"] = 300000
	report["large_history"] = sample(ctx, tasks, chat.ID, 200)
	var sync, fsync string
	must(db.QueryRowContext(ctx, `SHOW synchronous_commit`).Scan(&sync))
	must(db.QueryRowContext(ctx, `SHOW fsync`).Scan(&fsync))
	report["synchronous_commit"] = sync
	report["fsync"] = fsync
	var size int64
	must(db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&size))
	report["database_bytes"] = size
	var plan json.RawMessage
	must(db.QueryRowContext(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT * FROM fa.attempts WHERE run_id=md5('historical-run-49999') ORDER BY seq`).Scan(&plan))
	report["attempt_query_plan"] = plan
	closeAll()
	started = time.Now()
	_, closeReopen, err := harness.OpenTasks(ctx, data, harness.Adapters{})
	must(err)
	report["large_history_start_ms"] = time.Since(started).Milliseconds()
	closeReopen()
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))
}
