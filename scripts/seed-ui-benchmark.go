//go:build ignore

// Isolated, synthetic UI benchmark data. Never accepts a production path.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := os.Getenv("FREEAGENT_UI_TEST_ROOT")
	service := os.Getenv(postgres.ServiceFileEnv)
	if root == "" || service == "" {
		panic("requires the isolated UI/PG test runner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	data := filepath.Join(root, "data")
	cfg, err := harness.LoadConfig(data)
	must(err)
	cfg.MaxContextBytes = 4 << 20
	cfg.TaskSummaryBytes = 0
	must(harness.WriteConfig(data, cfg))
	conn, err := postgres.Read(filepath.Join(data, postgres.ConfigFile))
	must(err)
	admin, err := postgres.Read(service)
	must(err)
	admin.Database = conn.Database
	db, err := postgres.SQL(admin, "", false)
	must(err)
	defer db.Close()
	exec := func(query string, args ...any) { _, err := db.ExecContext(ctx, query, args...); must(err) }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec(`INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at)
 SELECT 'ui-bench-'||lpad(n::text,5,'0'),'基准会话 '||n,$1,'local',$2,$2 FROM generate_series(1,10000) n`, cfg.Workspace, now)
	raw, err := json.Marshal(cfg)
	must(err)
	exec(`INSERT INTO fa.runs(id,conversation_id,request_key,prompt,config_json,status,stop_reason,steps,elapsed_ms,output,created_at,updated_at)
 SELECT 'ui-run-'||n,'ui-bench-00001','seed-'||n,'合成输入 '||n,$1,'completed','',1,20,'合成回答 '||n,$2,$2 FROM generate_series(1,5000) n`, string(raw), now)
	exec(`INSERT INTO fa.messages(conversation_id,run_id,body_json) SELECT 'ui-bench-00001','ui-run-'||n,jsonb_build_object('Role',CASE WHEN m=1 THEN 'user' ELSE 'assistant' END,'Content',CASE WHEN m=1 THEN '第 '||n||' 轮合成输入：保留约束与来源。' ELSE '第 '||n||$1 END) FROM generate_series(1,5000) n CROSS JOIN generate_series(1,2) m`, " 轮合成回答。\n\n| 字段 | 内容 |\n| --- | --- |\n| 来源 | 合成基准 |\n\n公式 $x^2$。\n\n```go\nfmt.Println(\"synthetic\")\n```\n")
	exec(`ANALYZE fa.conversations; ANALYZE fa.runs; ANALYZE fa.messages`)
	fmt.Println(`{"conversations":10000,"turns":5000,"messages":10000,"synthetic":true}`)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
