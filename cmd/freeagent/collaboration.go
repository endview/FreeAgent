package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func interactiveCollaborationOwner(ctx context.Context, dataDir string) (*handoff.Client, bool, error) {
	_, _, _, err := handoff.Read(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("owner discovery is unavailable: %w", err)
	}
	client, err := interactiveOwner(ctx, dataDir)
	if err != nil {
		return nil, true, err
	}
	return client, true, nil
}

func runCollaboration(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || !slices.Contains([]string{"create", "continue", "inspect", "control", "adjust", "revise", "revisions", "increase-limits", "resolve", "preview", "confirm"}, args[0]) {
		return usageErr("usage: freeagent collab <create|continue|inspect|control|adjust|revise|revisions|increase-limits|resolve|preview|confirm> [flags]")
	}
	action := args[0]
	flags := flag.NewFlagSet("collab "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	parent := flags.String("parent", "", "parent collaboration id")
	child := flags.String("child", "", "child name")
	planPath := flags.String("plan", "", "explicit ordinary JSON plan")
	session := flags.String("session", "", "parent conversation; created when omitted")
	request := flags.String("request", "", "stable request key")
	control := flags.String("action", "", "pause, resume or cancel")
	specPath := flags.String("spec", "", "replacement undispatched child specification JSON")
	editPath := flags.String("edit", "", "bounded explained plan revision JSON")
	limitsPath := flags.String("limits", "", "finite increased parent execution limits JSON")
	digest := flags.String("digest", "", "exact inspect or preview digest")
	decision := flags.String("decision", "", "explicit conflict decision")
	interactive := flags.Bool("interactive", false, "ask exact confirmation during serial child execution")
	approve := flags.Bool("approve", false, "approve the child operation matching --digest; default declines")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("collab: unexpected arguments")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	if action == "create" && *planPath == "" {
		return usageErr("collab create: --plan is required")
	}
	if action != "create" && *parent == "" {
		return usageErr("collab: --parent is required")
	}
	if (action == "adjust" || action == "preview" || action == "confirm") && *child == "" {
		return usageErr("collab: --child is required")
	}
	allowed := map[string]bool{"data-dir": true, "parent": action != "create"}
	switch action {
	case "create":
		allowed["plan"], allowed["session"], allowed["request"] = true, true, true
	case "continue":
		allowed["interactive"] = true
	case "control":
		allowed["child"], allowed["action"] = true, true
	case "adjust":
		allowed["child"], allowed["spec"] = true, true
	case "revise":
		allowed["edit"] = true
	case "increase-limits":
		allowed["limits"] = true
	case "resolve":
		allowed["digest"], allowed["decision"] = true, true
	case "preview":
		allowed["child"] = true
	case "confirm":
		allowed["child"], allowed["digest"], allowed["approve"] = true, true, true
	}
	var invalid string
	flags.Visit(func(item *flag.Flag) {
		if !allowed[item.Name] {
			invalid = item.Name
		}
	})
	if invalid != "" {
		return fmt.Errorf("collab %s: --%s is not applicable", action, invalid)
	}
	if action == "control" && *control == "" {
		return usageErr("collab control: --action is required")
	}
	if action == "adjust" && *specPath == "" {
		return usageErr("collab adjust: --spec is required")
	}
	if action == "revise" && *editPath == "" {
		return usageErr("collab revise: --edit is required")
	}
	if action == "increase-limits" && *limitsPath == "" {
		return usageErr("collab increase-limits: --limits is required")
	}
	if action == "confirm" && *digest == "" {
		return usageErr("collab confirm: --digest from preview is required")
	}
	if action == "resolve" && (*digest == "" || *decision == "") {
		return usageErr("collab resolve: --digest and --decision are required")
	}

	var plan harness.CollaborationPlan
	var spec harness.ChildSpec
	var edit harness.CollaborationEdit
	var limits harness.ParentBudget
	if action == "create" {
		file, err := os.Open(*planPath)
		if err != nil {
			return err
		}
		plan, err = harness.DecodeCollaborationPlan(file)
		closeErr := file.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return err
		}
	}
	if action == "adjust" {
		if err := readBoundedJSON(*specPath, &spec); err != nil {
			return err
		}
	}
	if action == "revise" {
		if err := readBoundedJSON(*editPath, &edit); err != nil {
			return err
		}
	}
	if action == "increase-limits" {
		if err := readBoundedJSON(*limitsPath, &limits); err != nil {
			return err
		}
	}
	if client, attached, attachErr := interactiveCollaborationOwner(ctx, *dataDir); attached {
		if attachErr != nil {
			return attachErr
		}
		if *interactive {
			return errors.New("collab --interactive requires the owner CLI; attached collaboration uses the owner control surface")
		}
		command := harness.CollaborationCommand{Action: action, Parent: *parent, Child: *child,
			Session: *session, Request: *request, Control: *control, Digest: *digest,
			Decision: *decision, Approved: *approve}
		if action == "create" {
			command.Plan = &plan
		}
		if action == "adjust" {
			command.Spec = &spec
		}
		if action == "revise" {
			command.Edit = &edit
		}
		if action == "increase-limits" {
			command.Limits = &limits
		}
		raw, err := client.Collaboration(ctx, command)
		if err != nil {
			return err
		}
		_, err = stdout.Write(raw)
		if err == nil && (len(raw) == 0 || raw[len(raw)-1] != '\n') {
			_, err = io.WriteString(stdout, "\n")
		}
		return err
	}
	// Inspection and durable controls do not need model credentials or MCP
	// discovery. Only foreground execution assembles the selected adapters.
	var tasks *harness.TaskService
	var closeAll func()
	if action == "create" || action == "continue" || action == "preview" || action == "confirm" || action == "adjust" || action == "revise" {
		var err error
		tasks, closeAll, err = harness.OpenTasks(ctx, *dataDir, harness.Adapters{})
		if err != nil {
			return err
		}
	} else {
		config, err := harness.LoadConfig(*dataDir)
		if err != nil {
			return err
		}
		var store *harness.Store
		if action == "inspect" || action == "revisions" {
			store, err = openStoreForReading(ctx, *dataDir, stderr)
		} else {
			store, err = harness.OpenStore(ctx, *dataDir)
		}
		if err != nil {
			return err
		}
		closeAll = func() { _ = store.Close() }
		tasks = harness.NewTaskService(&harness.Service{Store: store, Config: config})
	}
	defer closeAll()
	tasks.SetEvents(func(event harness.Event) {
		if event.Kind == "collaboration" {
			_, _ = fmt.Fprintf(stderr, "[child %s] %s\n", event.Tool, event.Message)
		}
	})
	if *interactive {
		tasks.SetApproval(terminalApproval(newTerminalInput(ctx, stdin), stderr))
	}
	collab := harness.NewCollaboration(tasks)
	switch action {
	case "create":
		id, err := collab.Create(ctx, *session, *request, plan)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"parent_run": id, "continued": false})
	case "continue":
		report, err := collab.Continue(ctx, *parent)
		return errors.Join(err, writeJSON(stdout, report))
	case "inspect":
		report, err := collab.Inspect(ctx, *parent)
		if err != nil {
			return err
		}
		return writeJSON(stdout, report)
	case "control":
		if err := collab.Control(ctx, *parent, *child, *control); err != nil {
			return err
		}
	case "adjust":
		if err := collab.Adjust(ctx, *parent, *child, spec); err != nil {
			return err
		}
	case "revise":
		if err := collab.Revise(ctx, *parent, edit); err != nil {
			return err
		}
	case "revisions":
		revisions, err := collab.Revisions(ctx, *parent)
		if err != nil {
			return err
		}
		return writeJSON(stdout, revisions)
	case "increase-limits":
		if err := collab.IncreaseLimits(ctx, *parent, limits); err != nil {
			return err
		}
	case "resolve":
		if err := collab.Resolve(ctx, *parent, *digest, *decision); err != nil {
			return err
		}
	case "preview", "confirm":
		preview, err := collab.ChildPreview(ctx, *parent, *child)
		if err != nil {
			return err
		}
		if action == "preview" {
			return writeJSON(stdout, preview)
		}
		if *digest != preview.Digest {
			return harness.ErrApprovalChanged
		}
		result, err := collab.ChildConfirm(ctx, *parent, *child, preview, *approve)
		return errors.Join(err, writeJSON(stdout, result))
	}
	return writeJSON(stdout, map[string]any{"ok": true, "parent": *parent, "child": *child, "action": action})
}

func readBoundedJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return errors.New("freeagent: JSON specification exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("freeagent: JSON file must contain one object")
	}
	return nil
}
