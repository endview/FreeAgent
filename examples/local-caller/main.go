package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func main() {
	data := flag.String("data-dir", "", "existing lightweight Harness data directory")
	timeout := flag.Duration("timeout", 2*time.Minute, "caller cancellation bound")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	reader := bufio.NewReader(os.Stdin)
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{
		Emit: func(event harness.Event) {
			if event.Kind != "model_text" {
				fmt.Fprintln(os.Stderr, event.Kind, event.Tool)
			}
		},
		Approve: func(ctx context.Context, call harness.ToolCall, prepared harness.PreparedTool) (bool, error) {
			fmt.Fprintf(os.Stderr, "%s\n%s\nApprove exactly this operation? [y/N] ", call.Name, prepared.Preview)
			choice := make(chan string, 1)
			go func() { line, _ := reader.ReadString('\n'); choice <- line }()
			select {
			case line := <-choice:
				return strings.EqualFold(strings.TrimSpace(line), "y"), nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result, err := tasks.Submit(ctx, "", "", strings.Join(flag.Args(), " "))
	_ = json.NewEncoder(os.Stdout).Encode(result)
	closeAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
