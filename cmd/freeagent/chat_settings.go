package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

func chatCollaboration(ctx context.Context, tasks chatTasks, chat, choice string, reader *terminalInput, writer io.Writer) (*harness.CollaborationSettings, error) {
	settings, err := tasks.CollaborationSettings(ctx, chat)
	if err != nil {
		return nil, err
	}
	if choice == "once" {
		settings.Mode = "ask"
		_, _ = fmt.Fprintln(writer, "Review collaboration for the next task only.")
		return &settings, nil
	}
	if choice == "" {
		_, _ = fmt.Fprintf(writer, "Collaboration: %s\n1. Off\n2. Ask for each collaboration task\n3. Automatic orchestration\nSelection: ", settings.Mode)
		line, err := reader.read(ctx)
		if err != nil {
			return nil, err
		}
		choice = strings.TrimSpace(line)
	}
	switch choice {
	case "1", "off":
		settings.Mode = "off"
	case "2", "ask":
		settings.Mode = "ask"
	case "3", "auto":
		settings.Mode = "auto"
	default:
		return nil, errors.New("choose off, ask or auto")
	}
	if err := tasks.SetCollaborationSettings(ctx, chat, settings); err != nil {
		return nil, err
	}
	_, err = fmt.Fprintf(writer, "Chat collaboration: %s\n", settings.Mode)
	return nil, err
}

func reviewChatSuggestion(reader *terminalInput, writer io.Writer) harness.SuggestionReview {
	return func(ctx context.Context, suggestion harness.CollaborationSuggestion) (harness.CollaborationSuggestion, bool, error) {
		for {
			_, _ = fmt.Fprintln(writer, suggestion.String())
			for index, expert := range suggestion.Experts {
				_, _ = fmt.Fprintf(writer, "   %d tools: %s; files: %s\n", index+1, strings.Join(expert.Tools, ", "), strings.Join(expert.Paths, ", "))
			}
			_, _ = fmt.Fprint(writer, "Use this plan for this task? [y/N/e edit] ")
			answer, err := reader.read(ctx)
			if err != nil {
				return suggestion, false, err
			}
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "y":
				return suggestion, true, nil
			case "e":
				_, _ = fmt.Fprint(writer, "Expert number: ")
				line, err := reader.read(ctx)
				if err != nil {
					return suggestion, false, err
				}
				number, err := strconv.Atoi(strings.TrimSpace(line))
				if err != nil || number < 1 || number > len(suggestion.Experts) {
					_, _ = fmt.Fprintln(writer, "Choose a listed expert.")
					continue
				}
				_, _ = fmt.Fprint(writer, "Replacement objective: ")
				line, err = reader.read(ctx)
				if err != nil {
					return suggestion, false, err
				}
				suggestion.Experts[number-1].Objective = strings.TrimSpace(line)
				_, _ = fmt.Fprint(writer, "Model profile (blank keeps current): ")
				line, err = reader.read(ctx)
				if err != nil {
					return suggestion, false, err
				}
				if id := strings.TrimSpace(line); id != "" {
					suggestion.Experts[number-1].ModelID = id
				}
			default:
				return suggestion, false, nil
			}
		}
	}
}

func chatSettings(ctx context.Context, tasks chatTasks, chat, section string, reader *terminalInput, stdout, stderr io.Writer) error {
	if section == "" {
		_, _ = fmt.Fprint(stderr, "Settings\n1. Model\n2. Collaboration\n3. Advanced\nSelection: ")
		line, err := reader.read(ctx)
		if err != nil {
			return err
		}
		section = strings.TrimSpace(line)
	}
	switch section {
	case "1", "model":
		return chatModelSelection(ctx, tasks, chat, "", stdout, stderr)
	case "2", "collaboration":
		_, err := chatCollaboration(ctx, tasks, chat, "", reader, stderr)
		return err
	case "memory":
		return chatMemorySettings(ctx, tasks, reader, stderr)
	case "3", "advanced":
	default:
		return errors.New("choose a listed settings section")
	}
	settings, err := tasks.CollaborationSettings(ctx, chat)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(stderr, "Advanced\n1. Model pool\n2. Collaboration limits\n3. Expert tools and files\n4. Final synthesis\n5. Memory\nSelection: ")
	line, err := reader.read(ctx)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(line) {
	case "1":
		_, _ = fmt.Fprint(stderr, "Authorized model profiles, comma-separated (blank uses configured pool): ")
		line, err = reader.read(ctx)
		settings.Models = commaValues(line)
	case "2":
		values := []struct {
			name  string
			value *int
		}{
			{"Cumulative experts", &settings.Budget.MaxChildren},
			{"Concurrent experts", &settings.Budget.MaxConcurrentChildren},
			{"Model calls", &settings.Budget.MaxModelCalls},
			{"Tool calls", &settings.Budget.MaxToolCalls},
			{"Output bytes", &settings.Budget.MaxOutputBytes},
			{"Parent active seconds", &settings.Budget.MaxActiveSeconds},
			{"Automatic replans", &settings.MaxReplans},
			{"Expert steps", &settings.Child.MaxSteps},
			{"Expert active seconds", &settings.Child.MaxSeconds},
		}
		for _, value := range values {
			_, _ = fmt.Fprintf(stderr, "%s [%d]: ", value.name, *value.value)
			line, err = reader.read(ctx)
			if err != nil {
				return err
			}
			if line = strings.TrimSpace(line); line != "" {
				number, err := strconv.Atoi(line)
				if err != nil {
					return errors.New("limits require finite integers")
				}
				*value.value = number
			}
		}
	case "3":
		_, _ = fmt.Fprint(stderr, "Authorized expert tools, comma-separated (blank selects none): ")
		line, err = reader.read(ctx)
		if err != nil {
			return err
		}
		settings.Tools = commaValues(line)
		_, _ = fmt.Fprint(stderr, "Authorized workspace-relative file scopes, comma-separated (blank selects none): ")
		line, err = reader.read(ctx)
		settings.Paths = commaValues(line)
	case "4":
		_, _ = fmt.Fprint(stderr, "Use a bounded model call for final synthesis? [y/N] ")
		line, err = reader.read(ctx)
		settings.Synthesis = strings.EqualFold(strings.TrimSpace(line), "y")
	case "5":
		return chatMemorySettings(ctx, tasks, reader, stderr)
	default:
		return errors.New("choose a listed advanced section")
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(stderr, "Save to 1. This Chat 2. Future defaults [1]: ")
	line, err = reader.read(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) == "2" {
		err = tasks.SetDefaultCollaborationSettings(ctx, settings)
	} else {
		err = tasks.SetCollaborationSettings(ctx, chat, settings)
	}
	if err == nil {
		_, _ = fmt.Fprintln(stderr, "Settings saved.")
	}
	return err
}

func commaValues(text string) []string {
	var values []string
	for _, part := range strings.Split(strings.TrimSpace(text), ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}
