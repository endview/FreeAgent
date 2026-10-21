package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/endview/freeagent/internal/harness"
)

func chatModelSelection(ctx context.Context, tasks *harness.TaskService, conversationID, choice string, stdout, stderr io.Writer) error {
	config := tasks.Config()
	ids := make([]string, 0, len(config.Models))
	for id := range config.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if config.Auto.Enabled {
		ids = append(ids, "auto")
	}
	selected, err := tasks.SelectedModel(ctx, conversationID)
	if err != nil {
		return err
	}
	if choice == "" {
		for index, id := range ids {
			name := config.Models[id].Model
			if id == "auto" {
				name = "Auto"
			}
			if name == "" {
				name = config.Models[id].Provider
			}
			marker := ""
			if id == selected {
				marker = " (selected)"
			}
			_, _ = fmt.Fprintf(stdout, "%d. %s: %s%s\n", index+1, id, name, marker)
		}
		return nil
	}
	if number, err := strconv.Atoi(choice); err == nil && number >= 1 && number <= len(ids) {
		choice = ids[number-1]
	}
	if err := tasks.SelectModel(ctx, conversationID, choice); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stderr, "Model for subsequent tasks: %s. Any unfinished task retains its saved model and operations.\n", choice)
	return err
}

func chatIncreaseLimits(ctx context.Context, tasks *harness.TaskService, conversationID string, reader *terminalInput, writer io.Writer) error {
	run, err := tasks.Latest(ctx, conversationID)
	if err != nil {
		return err
	}
	if run.Status == "unknown" {
		return harness.ErrUnknownResult
	}
	limits, err := tasks.Limits(ctx, run.ID)
	if err != nil {
		return err
	}
	oldSteps, oldSeconds := limits.MaxSteps, limits.MaxRunSeconds
	limits.MaxSteps = min(100, limits.MaxSteps+12)
	limits.MaxRunSeconds = min(3600, limits.MaxRunSeconds+300)
	if limits.MaxSteps == oldSteps && limits.MaxRunSeconds == oldSeconds {
		return fmt.Errorf("supported finite limits have already been reached")
	}
	_, _ = fmt.Fprintf(writer, "Continue with limits of %d steps and %d seconds of active execution? Existing consumption is retained. [y/N] ", limits.MaxSteps, limits.MaxRunSeconds)
	answer, err := reader.read(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return fmt.Errorf("execution limits unchanged")
	}
	return tasks.IncreaseLimits(ctx, run.ID, limits)
}

func printChatResult(stdout, stderr io.Writer, result harness.RunResult, streamed bool) error {
	if streamed {
		_, _ = fmt.Fprintln(stdout)
	} else if result.Answer != "" {
		_, _ = fmt.Fprintln(stdout, result.Answer)
	}
	if result.Run.ID == "" {
		return nil
	}
	_, _ = fmt.Fprintf(stderr, "Task: %s; model: %s\n", result.Run.Status, result.Run.ModelID)
	if result.Run.Status != "completed" {
		_, _ = fmt.Fprintln(stderr, result.Run.StopReason)
	}
	printEvidence(stderr, result.Evidence)
	return nil
}

type chatCancellation struct {
	mu   sync.Mutex
	task context.CancelFunc
	idle context.CancelFunc
}

func (controller *chatCancellation) set(cancel context.CancelFunc) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.task = cancel
}

func (controller *chatCancellation) cancel() {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.task != nil {
		controller.task()
	} else {
		controller.idle()
	}
}

func printEvidence(writer io.Writer, evidence harness.Evidence) {
	if len(evidence.Files)+len(evidence.Commands)+len(evidence.HTTP)+len(evidence.ChecksNotRun)+len(evidence.Uncertainty) == 0 {
		return
	}
	_, _ = fmt.Fprintln(writer, "Runtime evidence:")
	for _, file := range evidence.Files {
		_, _ = fmt.Fprintf(writer, "  file %s: %s\n", file.Path, file.Status)
	}
	for _, command := range evidence.Commands {
		_, _ = fmt.Fprintf(writer, "  command %s: %s", command.Command, command.Status)
		if command.ExitCode != nil {
			_, _ = fmt.Fprintf(writer, " exit=%d", *command.ExitCode)
		}
		if command.Truncated {
			_, _ = fmt.Fprint(writer, " output=truncated")
		}
		_, _ = fmt.Fprintln(writer)
	}
	for _, check := range evidence.ChecksPassed {
		_, _ = fmt.Fprintln(writer, "  check passed:", check)
	}
	for _, request := range evidence.HTTP {
		_, _ = fmt.Fprintf(writer, "  GET %s: %s", request.URL, request.Status)
		if request.HTTPStatus != 0 {
			_, _ = fmt.Fprintf(writer, " HTTP=%d", request.HTTPStatus)
		}
		if request.Truncated {
			_, _ = fmt.Fprint(writer, " output=truncated")
		}
		_, _ = fmt.Fprintln(writer)
	}
	for _, check := range evidence.ChecksNotRun {
		_, _ = fmt.Fprintln(writer, "  check not run:", check)
	}
	for _, item := range evidence.Uncertainty {
		_, _ = fmt.Fprintln(writer, "  uncertain:", item)
	}
}
