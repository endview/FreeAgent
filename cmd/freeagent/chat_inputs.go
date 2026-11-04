package main

import (
	"context"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

func chatInputs(ctx context.Context, tasks *remoteChat, line string, chat *string, attachments *[]string, out io.Writer) (bool, error) {
	words := strings.Fields(line)
	if len(words) == 0 {
		return false, nil
	}
	switch words[0] {
	case "/guide":
		if len(words) > 2 {
			return true, fmt.Errorf("/guide [queued-number-or-key]")
		}
		state, err := tasks.Inputs(ctx, *chat)
		if err != nil {
			return true, err
		}
		pending := queuedCLIInputs(state)
		if len(pending) == 0 {
			return true, fmt.Errorf("no queued messages are waiting")
		}
		key := pending[0].Key
		if len(words) == 2 {
			key = words[1]
			if n, err := strconv.Atoi(key); err == nil {
				if n < 1 || n > len(pending) {
					return true, fmt.Errorf("queued message number must be between 1 and %d", len(pending))
				}
				key = pending[n-1].Key
			}
		}
		run, err := tasks.Latest(ctx, *chat)
		if err != nil {
			return true, err
		}
		item, err := tasks.SteerInput(ctx, *chat, key, run.ID)
		if err == nil {
			fmt.Fprintf(out, "Guidance received for the current task; input=%s status=%s\n", item.Key, item.Status)
		}
		return true, err
	case "/inputs", "/queue":
		if len(words) == 1 {
			state, err := tasks.Inputs(ctx, *chat)
			if err != nil {
				return true, err
			}
			fmt.Fprintf(out, "Queue paused=%v\n", state.Paused)
			for index, input := range queuedCLIInputs(state) {
				fmt.Fprintf(out, "  %d. %s  [/guide %d]\n", index+1, input.Prompt, index+1)
			}
			for _, i := range state.Entries {
				fmt.Fprintf(out, "%s %s %s: %s\n", i.Key, i.Action, i.Status, i.Prompt)
			}
			return true, nil
		}
		fallthrough
	case "/steer":
		action := "queue"
		target := ""
		if words[0] == "/steer" {
			action = "steer"
			run, err := tasks.Latest(ctx, *chat)
			if err != nil {
				return true, err
			}
			target = run.ID
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(line, words[0]))
		if prompt == "" {
			return true, fmt.Errorf("input text is required")
		}
		item, err := tasks.AcceptInput(ctx, *chat, harness.InputCommand{Action: action, RunID: target, RequestKey: fmt.Sprintf("input-%x", time.Now().UnixNano()), Prompt: prompt, Attachments: append([]string(nil), (*attachments)...)})
		if err == nil {
			*attachments = nil
			fmt.Fprintf(out, "Input %s: %s; key=%s\n", action, item.Status, item.Key)
		}
		return true, err
	case "/queue-pause", "/queue-resume":
		return true, tasks.PauseQueue(ctx, *chat, words[0] == "/queue-pause")
	case "/queue-cancel":
		if len(words) != 2 {
			return true, fmt.Errorf("/queue-cancel <input-key>")
		}
		item, err := tasks.CancelInput(ctx, *chat, words[1])
		fmt.Fprintln(out, item.Status)
		return true, err
	case "/branch", "/branch-reply":
		if len(words) < 2 {
			return true, fmt.Errorf("/branch <message-id> [edited text] or /branch-reply <message-id>")
		}
		id, err := strconv.ParseInt(words[1], 10, 64)
		if err != nil {
			return true, err
		}
		command := harness.BranchCommand{RequestKey: fmt.Sprintf("branch-%x", time.Now().UnixNano()), MessageID: id, Mode: "rerun"}
		if words[0] == "/branch-reply" {
			command.Mode = "continue"
		}
		if len(words) > 2 {
			text := strings.TrimSpace(strings.SplitN(line, words[1], 2)[1])
			command.Prompt = &text
		}
		result, err := tasks.BranchConversation(ctx, *chat, command)
		if err != nil {
			return true, err
		}
		*chat = result.Conversation.ID
		*attachments = nil
		for _, ref := range result.Attachments {
			*attachments = append(*attachments, ref.ID)
		}
		fmt.Fprintf(out, "Branch %s from message %d. Files and external effects were not rolled back.\n", *chat, id)
		if result.Prompt != "" {
			requestKey := command.RequestKey + "-start"
			options := harness.RunOptions{Attachments: append([]string(nil), (*attachments)...)}
			if err := tasks.SubmitWithOptions(ctx, *chat, requestKey, result.Prompt, options); err != nil {
				return true, err
			}
			*attachments = nil
			tasks.bind(requestKey)
			run, err := tasks.wait(ctx, *chat, requestKey, "")
			if err == nil {
				run, err = tasks.settle(ctx, run, options)
			}
			if err != nil {
				return true, err
			}
			return true, printChatResult(out, out, run, false)
		}
		return true, nil
	}
	return false, nil
}

func (remote *remoteChat) liveInput(ctx context.Context, chat, key, runID, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if line == "/stop" {
		return remote.CancelRequest(ctx, chat, key)
	}
	if strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "/steer ") && !strings.HasPrefix(line, "/guide ") && line != "/guide" && !strings.HasPrefix(line, "/queue ") && line != "/inputs" && line != "/queue" && !strings.HasPrefix(line, "/queue-") {
		return fmt.Errorf("while running, text is queued; use /guide [number], /steer <text>, /inputs or /stop")
	}
	var attachments []string
	if handled, err := chatInputs(ctx, remote, line, &chat, &attachments, remote.stderr); handled {
		return err
	}
	item, err := remote.AcceptInput(ctx, chat, harness.InputCommand{Action: "queue", RequestKey: fmt.Sprintf("input-%x", time.Now().UnixNano()), Prompt: line})
	if err == nil {
		fmt.Fprintf(remote.stderr, "Queued: %s\nUse /guide to guide with the next queued message, or /inputs to choose another. key=%s\n", line, item.Key)
	}
	return err
}

func queuedCLIInputs(state harness.InputState) []harness.PendingInput {
	items := make([]harness.PendingInput, 0)
	for _, input := range state.Entries {
		if input.Action == "queue" && input.Status == "pending" {
			items = append(items, input)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].MessageID < items[j].MessageID })
	return items
}
