package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"strings"
)

func runAnalysis(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] != "preview" && args[0] != "start" {
		return usageErr("analyze <preview|start> --skill ID --session ID --run ID[,ID] [--digest HASH --request KEY]")
	}
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	flags.SetOutput(errOut)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	chat := flags.String("session", "", "source conversation")
	skill := flags.String("skill", "", "installed, enabled source-analysis Skill")
	model := flags.String("model", "", "explicit model (defaults to current conversation)")
	runs := flags.String("run", "", "explicit selected Run IDs, comma separated")
	digest := flags.String("digest", "", "source preview digest")
	key := flags.String("request", "", "stable analysis request key")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if *skill == "" || *chat == "" || *runs == "" || flags.NArg() != 0 {
		return usageErr("analyze requires --skill, --session and --run")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	client, err := interactiveOwner(ctx, *data)
	if err != nil {
		return fmt.Errorf("analyze requires a running GUI/web/chat Owner: %w", err)
	}
	selection := harness.SourceAnalysisSelection{Skill: *skill, ModelID: *model, RunIDs: strings.Split(*runs, ",")}
	if args[0] == "preview" {
		preview, err := client.PreviewSourceAnalysis(ctx, *chat, selection)
		if err != nil {
			return err
		}
		return writeJSON(out, preview)
	}
	if *key == "" || *digest == "" {
		return usageErr("preview the actual sources first, then provide --digest and a stable --request")
	}
	remote := &remoteChat{Client: client, data: *data, stderr: errOut}
	state, err := client.State(ctx)
	if err != nil {
		return err
	}
	remote.cursor = state.LastSeq
	if err := client.AnalyzeSources(ctx, *chat, *key, selection, *digest); err != nil {
		return err
	}
	result, err := remote.wait(ctx, *chat, *key, "")
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}
