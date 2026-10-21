package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

const maxKeyInput = 4096

var errNotConsole = errors.New("stdin is not a console")

const keyUsage = "usage: freeagent key <list|add|edit|remove|test|models> [flags]; use --help"

func keyStore() (*harness.KeyStore, error) {
	path, err := apppaths.KeysFile()
	if err != nil {
		return nil, err
	}
	return harness.NewKeyStore(path), nil
}

// runKey manages key profiles. It touches only the keys file and its own
// lock, so it works while the desktop app owns the data directory; the one
// exception is remove --disable-models, which edits config.json.
func runKey(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || !slices.Contains([]string{"list", "add", "edit", "remove", "test", "models"}, args[0]) {
		return usageErr(keyUsage)
	}
	action, rest := args[0], args[1:]
	needsRef := action != "list" && action != "add"
	ref := ""
	if needsRef && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		ref, rest = rest[0], rest[1:]
	}
	flags := flag.NewFlagSet("key "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var asJSON, keyStdin, keyPrompt, noKey, force, disableModels bool
	var name, address, note, keyEnv, preset, newDefault string
	dataDir := defaultDataDir()
	flags.StringVar(&dataDir, "data-dir", dataDir, "data directory whose owner should receive mutations")
	switch action {
	case "list", "test", "models":
		flags.BoolVar(&asJSON, "json", false, "write structured output")
	case "add", "edit":
		flags.StringVar(&name, "name", "", "profile name")
		flags.StringVar(&address, "url", "", "service address, for example https://api.example.com/v1")
		flags.StringVar(&note, "note", "", "optional note")
		flags.BoolVar(&keyStdin, "key-stdin", false, "read the key from stdin (one line, at most 4096 bytes)")
		flags.StringVar(&keyEnv, "key-env", "", "read the key from this environment variable when it is used")
		flags.BoolVar(&noKey, "no-key", false, "no key; loopback addresses only")
		if action == "add" {
			flags.StringVar(&preset, "preset", "", "fill name and address from a preset: deepseek")
		} else {
			flags.BoolVar(&keyPrompt, "key-prompt", false, "enter a new key with hidden input")
		}
	case "remove":
		flags.BoolVar(&force, "force", false, "remove even if models use it; those models become missing")
		flags.BoolVar(&disableModels, "disable-models", false, "remove the models that use it first")
		flags.StringVar(&newDefault, "new-default", "", "new default model when --disable-models removes the default")
	}
	if err := parseFlags(flags, rest); err != nil {
		return err
	}
	if needsRef && ref == "" && flags.NArg() == 1 {
		ref = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageErr(keyUsage)
	}
	if needsRef && strings.TrimSpace(ref) == "" {
		return usagef("key %s: a key name or id is required", action)
	}
	store, err := keyStore()
	if err != nil {
		return err
	}
	switch action {
	case "list":
		return keyList(store, asJSON, stdout, stderr)
	case "add":
		return keyAdd(ctx, store, dataDir, harness.KeyInput{Name: name, BaseURL: address, Note: note}, preset,
			keySource{stdin: keyStdin, env: keyEnv, none: noKey}, stdin, stdout, stderr)
	case "edit":
		return keyEdit(ctx, store, dataDir, ref, flags, harness.KeyInput{Name: name, BaseURL: address, Note: note},
			keySource{stdin: keyStdin, env: keyEnv, none: noKey, prompt: keyPrompt}, stdin, stdout, stderr)
	case "remove":
		return keyRemove(ctx, store, dataDir, ref, force, disableModels, newDefault, stdout, stderr)
	}
	return keyProbe(ctx, store, dataDir, ref, action == "models", asJSON, stdout, stderr)
}

func keyList(store *harness.KeyStore, asJSON bool, stdout, stderr io.Writer) error {
	views, err := store.List()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, views)
	}
	if len(views) == 0 {
		_, _ = fmt.Fprintln(stderr, "No keys yet. Add one: freeagent key add --name <name> --url <address>")
		return nil
	}
	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tNAME\tADDRESS\tSOURCE\tHINT\tLAST CHECK")
	for _, view := range views {
		hint := view.Hint
		if hint == "" {
			hint = "-"
		}
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", view.ID, view.Name, view.BaseURL, keySourceText(view), hint, lastCheckText(view.LastCheck))
	}
	return table.Flush()
}

func keySourceText(view harness.KeyProfileView) string {
	switch view.Source {
	case harness.KeySourceEnv:
		return "env " + view.EnvName
	case harness.KeySourceSaved:
		if !view.Encrypted {
			return "saved (unencrypted)"
		}
	}
	return view.Source
}

func lastCheckText(check *harness.KeyCheck) string {
	if check == nil {
		return "never"
	}
	return check.Result + " " + check.At.Local().Format(time.DateTime)
}

// keySource holds the mutually exclusive ways to give a key.
type keySource struct {
	stdin, none, prompt bool
	env                 string
}

func (source keySource) count() int {
	count := 0
	for _, set := range []bool{source.stdin, source.none, source.prompt, source.env != ""} {
		if set {
			count++
		}
	}
	return count
}

// apply fills input from the chosen source. Without one, add prompts and edit
// keeps the current key.
func (source keySource) apply(ctx context.Context, input *harness.KeyInput, prompt bool, stdin io.Reader, stderr io.Writer) error {
	if source.count() > 1 {
		return usageErr("choose only one of --key-prompt, --key-stdin, --key-env and --no-key")
	}
	var err error
	switch {
	case source.stdin:
		input.Source = harness.KeySourceSaved
		input.Secret, err = readKeyStdin(stdin)
	case source.env != "":
		input.Source, input.EnvName = harness.KeySourceEnv, source.env
	case source.none:
		input.Source = harness.KeySourceNone
	case source.prompt || prompt:
		input.Source = harness.KeySourceSaved
		input.Secret, err = promptKey(ctx, stdin, stderr)
	}
	return err
}

func interactiveKeyOwner(ctx context.Context, dataDir string) (*handoff.Client, bool, error) {
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

func tryAttachedKeyMutation(ctx context.Context, dataDir string, request harness.KeyControlRequest, stdout, stderr io.Writer) (bool, error) {
	client, attached, err := interactiveKeyOwner(ctx, dataDir)
	if !attached {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	result, err := client.ControlKeyMutation(ctx, request)
	if err != nil {
		return true, err
	}
	if !result.Applied {
		return true, errors.New("attached key operation was not applied")
	}
	if request.Remove {
		return true, writeJSON(stdout, map[string]any{"removed": request.Ref, "operation": result.Operation.ID})
	}
	view, findErr := keyStoreFindAfterMutation(request)
	if findErr != nil {
		return true, findErr
	}
	if err := writeJSON(stdout, view); err != nil {
		return true, err
	}
	if view.Source == harness.KeySourceSaved && !view.Encrypted {
		_, _ = fmt.Fprintf(stderr, "The key is stored unencrypted in the local key store.\n")
	}
	return true, nil
}

func keyStoreFindAfterMutation(request harness.KeyControlRequest) (harness.KeyProfileView, error) {
	store, err := keyStore()
	if err != nil {
		return harness.KeyProfileView{}, err
	}
	if request.Ref != "" {
		return store.Find(request.Ref)
	}
	return store.Find(request.Input.Name)
}

func tryAttachedKeyProbe(ctx context.Context, dataDir, ref string) (bool, harness.ProbeResult, error) {
	client, attached, err := interactiveKeyOwner(ctx, dataDir)
	if !attached {
		return false, harness.ProbeResult{}, nil
	}
	if err != nil {
		return true, harness.ProbeResult{}, err
	}
	result, err := client.ControlKeyProbe(ctx, ref)
	return true, result, err
}

func keyAdd(ctx context.Context, store *harness.KeyStore, dataDir string, input harness.KeyInput, preset string, source keySource,
	stdin io.Reader, stdout, stderr io.Writer) error {
	suggested := ""
	if preset != "" {
		index := slices.IndexFunc(harness.KeyPresets(), func(item harness.KeyPreset) bool { return strings.EqualFold(item.ID, preset) })
		if index < 0 {
			return usagef("key add: unknown preset %q; available: deepseek", preset)
		}
		chosen := harness.KeyPresets()[index]
		if input.Name == "" {
			input.Name = chosen.Name
		}
		if input.BaseURL == "" {
			input.BaseURL = chosen.BaseURL
		}
		suggested = chosen.Model
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.BaseURL) == "" {
		return usageErr("key add: --name and --url are required (or --preset)")
	}
	if err := source.apply(ctx, &input, true, stdin, stderr); err != nil {
		return err
	}
	if attached, err := tryAttachedKeyMutation(ctx, dataDir, harness.KeyControlRequest{Input: input}, stdout, stderr); attached {
		clear(input.Secret)
		return err
	}
	view, err := store.Add(input)
	if err != nil {
		return fmt.Errorf("key add: %w", err)
	}
	if err := writeJSON(stdout, map[string]string{"id": view.ID, "name": view.Name}); err != nil {
		return err
	}
	if view.Source == harness.KeySourceSaved && !view.Encrypted {
		_, _ = fmt.Fprintf(stderr, "The key is stored unencrypted in %s on this system.\n", store.Path())
	}
	model := "<model>"
	if suggested != "" {
		model = suggested
		_, _ = fmt.Fprintf(stderr, "Suggested model: %s. Prefer a name listed by: freeagent key models %s\n", suggested, shellArg(view.Name))
	}
	_, _ = fmt.Fprintf(stderr, "Next: freeagent config model --id <id> --key %s --model %s\n", shellArg(view.Name), model)
	return nil
}

func keyEdit(ctx context.Context, store *harness.KeyStore, dataDir, ref string, flags *flag.FlagSet, changes harness.KeyInput,
	source keySource, stdin io.Reader, stdout, stderr io.Writer) error {
	current, err := store.Find(ref)
	if err != nil {
		return fmt.Errorf("key edit: %w: %q", err, ref)
	}
	input := harness.KeyInput{Name: current.Name, BaseURL: current.BaseURL, Note: current.Note}
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "name":
			input.Name = changes.Name
		case "url":
			input.BaseURL = changes.BaseURL
		case "note":
			input.Note = changes.Note
		}
	})
	if err := source.apply(ctx, &input, false, stdin, stderr); err != nil {
		return err
	}
	if attached, err := tryAttachedKeyMutation(ctx, dataDir, harness.KeyControlRequest{Ref: current.ID, Input: input}, stdout, stderr); attached {
		clear(input.Secret)
		return err
	}
	view, err := store.Update(current.ID, input)
	if err != nil {
		return fmt.Errorf("key edit: %w", err)
	}
	return writeJSON(stdout, view)
}

func keyRemove(ctx context.Context, store *harness.KeyStore, dataDir, ref string, force, disableModels bool, newDefault string,
	stdout, stderr io.Writer) error {
	view, err := store.Find(ref)
	if err != nil {
		return fmt.Errorf("key remove: %w: %q", err, ref)
	}
	if force && disableModels {
		return usageErr("key remove: choose --force or --disable-models")
	}
	if newDefault != "" && !disableModels {
		return usageErr("key remove: --new-default needs --disable-models")
	}
	dataDir, err = dataDirOrError(dataDir)
	if err != nil {
		return err
	}
	if attached, err := tryAttachedKeyMutation(ctx, dataDir, harness.KeyControlRequest{Ref: view.ID, Remove: true,
		Force: force, DisableModels: disableModels, NewDefault: newDefault}, stdout, stderr); attached {
		return err
	}
	users, err := keyUsers(dataDir, view.ID)
	if err != nil {
		return err
	}
	var removed []string
	switch {
	case len(users) == 0:
	case disableModels:
		path, err := filepath.Abs(dataDir)
		if err != nil {
			return err
		}
		var changes []string
		_, err = harness.Configure(ctx, path, func(config *harness.Config) error {
			removed = usersOf(*config, view.ID)
			if len(removed) == 0 {
				return nil
			}
			if err := checkNewDefault(*config, removed, newDefault); err != nil {
				return err
			}
			var removeErr error
			changes, removeErr = harness.RemoveModels(config, removed, newDefault)
			if removeErr != nil {
				return &exitError{code: exitUsage, err: fmt.Errorf("key remove: %w", removeErr)}
			}
			return nil
		})
		if err != nil {
			return err
		}
		writeChanges(stderr, changes)
	case force:
		_, _ = fmt.Fprintf(stderr, "These models keep referring to the removed key and become missing: %s\n", strings.Join(users, ", "))
	default:
		_, _ = fmt.Fprintf(stderr, "Key %q is used by models: %s\n", view.Name, strings.Join(users, ", "))
		_, _ = fmt.Fprintln(stderr, "Remove those models too with --disable-models, or keep them as missing with --force.")
		return &exitError{code: exitFailure}
	}
	if err := store.Remove(view.ID); err != nil {
		return fmt.Errorf("key remove: %w", err)
	}
	if removed == nil {
		removed = []string{}
	}
	return writeJSON(stdout, map[string]any{"removed": view.ID, "name": view.Name, "removed_models": removed})
}

// keyUsers lists the models of dataDir that use the key profile. A data
// directory that was never initialized has none.
func keyUsers(dataDir, keyID string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(dataDir, "config.json")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	config, err := harness.LoadConfig(dataDir)
	if err != nil {
		return nil, err
	}
	return usersOf(config, keyID), nil
}

func usersOf(config harness.Config, keyID string) []string {
	var users []string
	for id, model := range config.Models {
		if model.Key == keyID {
			users = append(users, id)
		}
	}
	slices.Sort(users)
	return users
}

// checkNewDefault gives the flag to use when removing models takes the
// default away while other models remain.
func checkNewDefault(config harness.Config, removed []string, newDefault string) error {
	if newDefault == "" && slices.Contains(removed, config.DefaultModel) && len(config.Models) > len(removed) {
		return usagef("model %q is the default; choose the new default with --new-default <id>", config.DefaultModel)
	}
	return nil
}

func writeChanges(stderr io.Writer, changes []string) {
	for _, change := range changes {
		_, _ = fmt.Fprintln(stderr, change)
	}
}

// keyProbe runs one GET <base>/models and records the category as the
// profile's last check. Anything but ok exits 1.
func keyProbe(ctx context.Context, store *harness.KeyStore, dataDir, ref string, listModels, asJSON bool, stdout, stderr io.Writer) error {
	view, err := store.Find(ref)
	if err != nil {
		return fmt.Errorf("key: %w: %q", err, ref)
	}
	if attached, result, attachErr := tryAttachedKeyProbe(ctx, dataDir, view.ID); attached {
		if attachErr != nil {
			return attachErr
		}
		return printKeyProbe(result, view, listModels, asJSON, stdout, stderr)
	}
	resolved, err := store.Resolve(view.ID)
	if errors.Is(err, harness.ErrKeyNeedsReentry) {
		_, _ = fmt.Fprintf(stderr, "Enter the key again: freeagent key edit %s --key-prompt\n", shellArg(view.Name))
		return &exitError{code: exitFailure, err: err}
	}
	if err != nil {
		return err
	}
	result, err := harness.ProbeModels(ctx, resolved, nil)
	if err != nil {
		return err
	}
	recordErr := store.RecordCheck(view.ID, result.Category)
	return printKeyProbeWithCheck(result, view, listModels, asJSON, stdout, stderr, recordErr)
}

func printKeyProbe(result harness.ProbeResult, view harness.KeyProfileView, listModels, asJSON bool, stdout, stderr io.Writer) error {
	return printKeyProbeWithCheck(result, view, listModels, asJSON, stdout, stderr, nil)
}

func printKeyProbeWithCheck(result harness.ProbeResult, view harness.KeyProfileView, listModels, asJSON bool, stdout, stderr io.Writer, recordErr error) error {
	var printErr error
	switch {
	case asJSON:
		printErr = writeJSON(stdout, map[string]any{"id": view.ID, "name": view.Name, "category": result.Category,
			"status": result.Status, "models": nonNil(result.Models), "detail": result.Detail})
	case result.Category == "ok" && listModels:
		for _, model := range result.Models {
			_, _ = fmt.Fprintln(stdout, model)
		}
	case result.Category == "ok":
		_, printErr = fmt.Fprintf(stdout, "ok: %d models\n", len(result.Models))
	default:
		line := result.Category
		if result.Status != 0 {
			line += " (HTTP " + strconv.Itoa(result.Status) + ")"
		}
		if result.Detail != "" {
			line += ": " + result.Detail
		}
		_, printErr = fmt.Fprintln(stdout, line)
	}
	if result.Category == "not_found" || result.Category == "not_models" {
		_, _ = fmt.Fprintf(stderr, "This service does not list models. Set the model name directly: freeagent config model --id <id> --key %s --model <model>\n", shellArg(view.Name))
	}
	if err := errors.Join(printErr, recordErr); err != nil {
		return err
	}
	if result.Category != "ok" {
		return &exitError{code: exitFailure}
	}
	return nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// shellArg quotes a profile name for a copyable command line.
func shellArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\"'`$&|;<>()") {
		return value
	}
	return strconv.Quote(value)
}

// readKeyStdin reads one key from piped stdin: up to EOF, one trailing line
// break removed, at most 4096 bytes. Errors never repeat the input.
func readKeyStdin(stdin io.Reader) ([]byte, error) {
	buffer := make([]byte, maxKeyInput+3)
	n, err := io.ReadFull(stdin, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		clear(buffer)
		return nil, fmt.Errorf("read key from stdin: %w", err)
	}
	if n == len(buffer) {
		clear(buffer)
		return nil, usageErr("the key from stdin exceeds 4096 bytes")
	}
	key := buffer[:n]
	if bytes.HasSuffix(key, []byte("\r\n")) {
		key = key[:len(key)-2]
	} else if bytes.HasSuffix(key, []byte("\n")) {
		key = key[:len(key)-1]
	}
	switch {
	case len(key) == 0:
		clear(buffer)
		return nil, usageErr("no key on stdin")
	case bytes.ContainsAny(key, "\r\n"):
		clear(buffer)
		return nil, usageErr("the key from stdin must be a single line")
	case len(key) > maxKeyInput:
		clear(buffer)
		return nil, usageErr("the key from stdin exceeds 4096 bytes")
	}
	return key, nil
}

// promptKey reads one line from the console with echo off. Echo comes back
// when the line is read, on error, and when the command is interrupted.
func promptKey(ctx context.Context, stdin io.Reader, stderr io.Writer) ([]byte, error) {
	restore, err := disableEcho(stdin)
	if errors.Is(err, errNotConsole) {
		return nil, usageErr("stdin is not a console; pass the key with --key-stdin, or name a variable with --key-env")
	}
	if err != nil {
		return nil, fmt.Errorf("hide key input: %w", err)
	}
	var once sync.Once
	restoreEcho := func() { once.Do(restore) }
	defer restoreEcho()
	_, _ = io.WriteString(stderr, "API key (input hidden): ")
	type line struct {
		key []byte
		err error
	}
	done := make(chan line, 1)
	go func() {
		key, err := readKeyLine(stdin)
		done <- line{key, err}
	}()
	select {
	case result := <-done:
		_, _ = io.WriteString(stderr, "\n")
		return result.key, result.err
	case <-ctx.Done():
		restoreEcho()
		_, _ = io.WriteString(stderr, "\n")
		return nil, ctx.Err()
	}
}

// readKeyLine reads up to a line break into one fixed buffer, so the key is
// never copied by a growing slice.
func readKeyLine(stdin io.Reader) ([]byte, error) {
	buffer := make([]byte, 0, maxKeyInput+1)
	one := make([]byte, 1)
	for {
		n, err := stdin.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			if len(buffer) == cap(buffer) {
				clear(buffer)
				return nil, usageErr("the key exceeds 4096 bytes")
			}
			buffer = append(buffer, one[0])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			clear(buffer)
			return nil, fmt.Errorf("read key: %w", err)
		}
	}
	one[0] = 0
	buffer = bytes.TrimSuffix(buffer, []byte("\r"))
	if len(buffer) == 0 {
		return nil, usageErr("no key entered")
	}
	if len(buffer) > maxKeyInput {
		clear(buffer)
		return nil, usageErr("the key exceeds 4096 bytes")
	}
	return buffer, nil
}
