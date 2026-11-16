package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func runConversationManagement(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return usageErr("conversations <list|preview|apply|finish|result> [--view active|trash|retained --session ID --action trash|restore|purge|move|rename|pin|unpin|delete_space --space ID --operation PREVIEW_ID --yes]")
	}
	f := flag.NewFlagSet("conversations", flag.ContinueOnError)
	f.SetOutput(errOut)
	data := f.String("data-dir", defaultDataDir(), "data directory")
	view := f.String("view", "active", "active, trash or retained")
	space := f.String("space", "", "space ID")
	query := f.String("query", "", "title contains")
	entry := f.String("entry", "", "local, telegram, lark, qq or unrecorded")
	before := f.String("before", "", "last execution activity before UTC timestamp")
	cursor := f.String("cursor", "", "page cursor")
	filter := f.String("filter", "", "pinned, unpinned, empty, attachments, branch, blocked")
	sortBy := f.String("sort", "", "bytes or runs")
	session := f.String("session", "", "comma separated exact conversation IDs")
	action := f.String("action", "", "preview action")
	title := f.String("title", "", "new title")
	replacement := f.String("replacement", "", "new or existing replacement conversation")
	cancel := f.Bool("cancel-pending", false, "cancel queued input and unsent deliveries")
	keep := f.Bool("keep-conversations", false, "delete space but keep conversations")
	operation := f.String("operation", "", "exact preview ID")
	yes := f.Bool("yes", false, "confirm the exact operation ID")
	limit := f.Int("limit", 60, "page size 1..500")
	if err := parseFlags(f, args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return usageErr("unexpected arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{})
	var client *handoff.Client
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err = interactiveOwner(ctx, *data)
	} else if err == nil {
		defer closeAll()
	}
	if err != nil {
		return err
	}
	switch args[0] {
	case "result":
		if *operation == "" {
			return usageErr("result --operation ID")
		}
		var result harness.ManagementResult
		var e error
		if client != nil {
			result, e = client.ConversationManagementResult(ctx, *operation)
		} else {
			result, e = tasks.ConversationManagementResult(ctx, *operation)
		}
		if e != nil {
			return e
		}
		return writeJSON(out, result)
	case "list":
		if client != nil {
			q := url.Values{"view": {*view}, "space_id": {*space}, "q": {*query}, "cursor": {*cursor}, "entry": {*entry}, "before": {*before}, "filter": {*filter}, "sort": {*sortBy}, "limit": {strconv.Itoa(*limit)}}
			result, e := client.ManageConversations(ctx, q)
			if e != nil {
				return e
			}
			return writeJSON(out, result)
		}
		result, e := tasks.ManageConversations(ctx, harness.ManagementQuery{View: *view, SpaceID: *space, Query: *query, Cursor: *cursor, Entry: *entry, Before: *before, Filter: *filter, Sort: *sortBy, Limit: *limit})
		if e != nil {
			return e
		}
		return writeJSON(out, result)
	case "preview":
		cmd := harness.ManagementCommand{Action: *action, SpaceID: *space, Title: *title, Replacement: *replacement, CancelPending: *cancel}
		if *session != "" {
			cmd.IDs = strings.Split(*session, ",")
		}
		if *action == "delete_space" {
			trash := !*keep
			cmd.TrashConversations = &trash
		}
		var result harness.ManagementPreview
		var e error
		if client != nil {
			result, e = client.PreviewConversationManagement(ctx, cmd)
		} else {
			result, e = tasks.PreviewConversationManagement(ctx, cmd)
		}
		if e != nil {
			return e
		}
		return writeJSON(out, result)
	case "apply":
		if !*yes || *operation == "" {
			return usageErr("review a preview, then use apply --operation ID --yes")
		}
		var result harness.ManagementResult
		var e error
		if client != nil {
			result, e = client.ApplyConversationManagement(ctx, *operation)
		} else {
			var control *harness.ControlService
			control, e = tasks.Control()
			if e == nil {
				var receipt harness.ControlResult
				receipt, e = control.ApplyDirectControlChange(ctx, harness.ControlManageConversations, map[string]any{"preview_id": *operation, "command": harness.ManagementCommand{}}, "", "cli")
				if e == nil {
					e = json.Unmarshal(receipt.Data, &result)
				}
			}
		}
		if e != nil {
			return e
		}
		return writeJSON(out, result)
	case "finish":
		if !*yes || *operation == "" {
			return usageErr("finish --operation ID --yes")
		}
		if client != nil {
			err = client.FinishManagementTasks(ctx, *operation)
		} else {
			err = tasks.FinishManagementTasks(ctx, *operation)
		}
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]bool{"finished": true})
	}
	return usageErr("unknown conversations action")
}

func runStoreUpgrade(ctx context.Context, args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("store-upgrade", flag.ContinueOnError)
	f.SetOutput(errOut)
	data := f.String("data-dir", defaultDataDir(), "data directory")
	backup := f.String("backup", "", "new complete pre-upgrade backup directory")
	confirm := f.Bool("confirm", false, "confirm offline schema upgrade")
	if err := parseFlags(f, args); err != nil {
		return err
	}
	if !*confirm || *backup == "" || f.NArg() != 0 {
		return usageErr("store-upgrade --data-dir DATA --backup NEW_BACKUP --confirm (stop Owner first)")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	if err := harness.UpgradeConversationStore(ctx, *data, *backup); err != nil {
		return err
	}
	return writeJSON(out, map[string]any{"upgraded": true, "schema": 2, "backup": *backup})
}
