package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func mustSpaceJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func spaceOperation(action string, args harness.SpaceControlArguments) (string, error) {
	switch action {
	case "new":
		return harness.ControlCreateSpace, nil
	case "rename":
		return harness.ControlRenameSpace, nil
	case "delete":
		return harness.ControlDeleteSpace, nil
	case "permission":
		return harness.ControlSetSpacePermission, nil
	case "move":
		return harness.ControlMoveConversation, nil
	case "default":
		return harness.ControlSpaceDefault, nil
	case "active":
		return harness.ControlSpaceActive, nil
	case "multi":
		return harness.ControlSpaceMulti, nil
	case "person":
		return harness.ControlPersonPermission, nil
	}
	return "", usageErr("spaces <list|new|rename|delete|permission|move|default|active|multi|person> --space <id> [--title <name> --session <id> --mode none|ask|workspace|full --person <platform-id> --enabled --yes]")
}

func runSpaces(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("spaces", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Harness data directory")
	space := flags.String("space", harness.LocalSpaceID, "space ID")
	title := flags.String("title", "", "space title")
	chat := flags.String("session", "", "conversation ID")
	mode := flags.String("mode", "", "none, ask, workspace or full")
	person := flags.String("person", "", "stable platform person ID")
	enabled := flags.Bool("enabled", false, "enable multiple conversations")
	yes := flags.Bool("yes", false, "explicitly confirm this exact space change")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("spaces: unexpected arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	if action == "list" {
		store, err := openStoreForReading(ctx, *data, stderr)
		if err != nil {
			return err
		}
		defer store.Close()
		spaces, err := store.ListSpaces(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, spaces)
	}
	input := harness.SpaceControlArguments{SpaceID: *space, Title: *title, ConversationID: *chat, Mode: harness.PermissionMode(*mode), PersonID: *person, DisplayName: *title}
	if action == "multi" {
		input.Enabled = enabled
	}
	kind, err := spaceOperation(action, input)
	if err != nil {
		return err
	}
	_, _, core, closeAll, err := openRuntime(ctx, *data)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, e := interactiveOwner(ctx, *data)
		if e != nil {
			return e
		}
		return applyAttachedSpace(ctx, client, kind, input, *yes, newTerminalInput(ctx, stdin), stdout, stderr)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	control, err := core.Control()
	if err != nil {
		return err
	}
	op, err := control.PlanControlChange(ctx, harness.ControlPlanRequest{Kind: kind, Arguments: mustSpaceJSON(input), Authorization: harness.ControlUserDirect})
	if err != nil {
		return err
	}
	if !confirmSpaceInput(ctx, *yes, op.Preview, newTerminalInput(ctx, stdin), stderr) {
		return errControlDeclined
	}
	capability, err := control.IssueInteractiveCapability(ctx, "", "cli")
	if err != nil {
		return err
	}
	confirmation, err := control.RenderConfirmation(ctx, op.ID, "", "cli")
	if err != nil {
		return err
	}
	result, err := control.ConfirmControlChange(ctx, op.ID, harness.ControlAuthorization{Source: harness.ControlUserDirect, Principal: "cli", Capability: capability, Code: confirmation.Code})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func confirmSpaceInput(ctx context.Context, yes bool, preview string, reader *terminalInput, w io.Writer) bool {
	_, _ = fmt.Fprintln(w, preview)
	if yes {
		return true
	}
	_, _ = fmt.Fprint(w, "Confirm this change? [y/N] ")
	line, err := reader.read(ctx)
	return err == nil && strings.EqualFold(strings.TrimSpace(line), "y")
}

func applyAttachedSpace(ctx context.Context, client *handoff.Client, kind string, args harness.SpaceControlArguments, yes bool, reader *terminalInput, out, errOut io.Writer) error {
	op, err := client.ControlPlan(ctx, kind, mustSpaceJSON(args), "")
	if err != nil {
		return err
	}
	if !confirmSpaceInput(ctx, yes, op.Preview, reader, errOut) {
		_, _ = client.ControlReject(ctx, op.ID, "")
		return errControlDeclined
	}
	confirmation, err := client.ControlConfirmation(ctx, op.ID, "")
	if err != nil {
		return err
	}
	result, err := client.ControlConfirm(ctx, op.ID, "", confirmation.Code)
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}

func chooseChatSpace(spaces []harness.Space, value string) (harness.Space, error) {
	if number, err := strconv.Atoi(value); err == nil && number >= 1 && number <= len(spaces) {
		return spaces[number-1], nil
	}
	var found *harness.Space
	for _, space := range spaces {
		if space.ID == value {
			return space, nil
		}
		if space.Title == value {
			if found != nil {
				return harness.Space{}, errors.New("ambiguous space title; use its ID")
			}
			copy := space
			found = &copy
		}
	}
	if found != nil {
		return *found, nil
	}
	return harness.Space{}, harness.ErrSpaceNotFound
}

func handleChatSpaces(ctx context.Context, tasks *remoteChat, prompt string, chat *string, reader *terminalInput, out, errOut io.Writer) (bool, error) {
	fields := strings.Fields(prompt)
	if len(fields) == 0 || fields[0] != "/spaces" && fields[0] != "/space" && fields[0] != "/move" && fields[0] != "/people" {
		return false, nil
	}
	spaces, err := tasks.Spaces(ctx)
	if err != nil {
		return true, err
	}
	if tasks.space == "" {
		tasks.space = harness.LocalSpaceID
	}
	if fields[0] == "/spaces" || fields[0] == "/space" && len(fields) == 1 {
		for index, space := range spaces {
			_, _ = fmt.Fprintf(out, "%d. %s [%s] %s\n", index+1, space.Title, space.ID, space.Source)
		}
		return true, nil
	}
	args := harness.SpaceControlArguments{SpaceID: tasks.space, ConversationID: *chat}
	var kind string
	if fields[0] == "/move" {
		target, err := chooseChatSpace(spaces, strings.Join(fields[1:], " "))
		if err != nil {
			return true, err
		}
		args.SpaceID, kind = target.ID, harness.ControlMoveConversation
	} else if fields[0] == "/people" {
		if len(fields) != 4 || fields[1] != "set" {
			return true, errors.New("/people set <stable-platform-ID> <none|ask|workspace|full>")
		}
		kind = harness.ControlPersonPermission
		args = harness.SpaceControlArguments{PersonID: fields[2], Mode: harness.PermissionMode(fields[3])}
	} else {
		switch fields[1] {
		case "new", "rename":
			args.Title = strings.Join(fields[2:], " ")
			if fields[1] == "new" {
				kind = harness.ControlCreateSpace
			} else {
				kind = harness.ControlRenameSpace
			}
		case "delete":
			kind = harness.ControlDeleteSpace
		case "default":
			kind = harness.ControlSpaceDefault
		case "multi":
			if len(fields) != 3 || fields[2] != "on" && fields[2] != "off" {
				return true, errors.New("/space multi on|off")
			}
			enabled := fields[2] == "on"
			args.Enabled = &enabled
			kind = harness.ControlSpaceMulti
		case "permission":
			if len(fields) != 3 {
				return true, errors.New("/space permission none|ask|workspace|full")
			}
			kind = harness.ControlSetSpacePermission
			args.Mode = harness.PermissionMode(fields[2])
		default:
			space, err := chooseChatSpace(spaces, strings.Join(fields[1:], " "))
			if err != nil {
				return true, err
			}
			tasks.space = space.ID
			sessions, err := tasks.Conversations(ctx)
			if err != nil {
				return true, err
			}
			if len(sessions) > 0 {
				*chat = sessions[0].ID
			} else {
				created, err := tasks.NewConversation(ctx, "Interactive chat")
				if err != nil {
					return true, err
				}
				*chat = created.ID
			}
			_, _ = fmt.Fprintln(out, "Space:", space.Title)
			return true, nil
		}
	}
	err = applyAttachedSpace(ctx, tasks.Client, kind, args, false, reader, out, errOut)
	if err == nil && kind == harness.ControlDeleteSpace {
		tasks.space = harness.LocalSpaceID
	}
	if err == nil && kind == harness.ControlMoveConversation {
		tasks.space = args.SpaceID
	}
	return true, err
}
