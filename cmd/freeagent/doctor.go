package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type doctorCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
}

// Diagnosis reuses the normal config/store readers without opening an Owner,
// resolving credentials, starting MCP or making a Provider request. Independent
// failures stay in one report so a broken config does not hide a broken store.
func doctorReport(ctx context.Context, data string) (map[string]any, []doctorCheck, bool) {
	report := map[string]any{"ok": true, "data_dir": data, "network_tested": false,
		"credentials_tested": false, "encrypted": harness.KeysEncrypted()}
	checks := make([]doctorCheck, 0, 8)
	ok := true
	add := func(id, status, detail, action string) {
		checks = append(checks, doctorCheck{id, status, detail, action})
		ok = ok && status != "error"
	}
	config, configErr := harness.LoadConfig(data)
	if configErr != nil {
		detail := doctorError(configErr)
		report["config_error"] = detail
		add("config", "error", detail, "Check config.json, the current data format, workspace and instruction paths. For a fresh install, run init with a new --data-dir; keep existing data for recovery.")
	} else {
		report["workspace"], report["models"], report["default_model"] = config.Workspace, config.Models, config.DefaultModel
		add("config", "ready", "Current configuration and configured paths are valid.", "")
	}
	if err := harness.VerifyData(ctx, data); err != nil {
		detail := doctorError(err)
		report["store_error"] = detail
		add("store", "error", detail, "Check the selected data directory. For recovery, verify a backup with backup-verify and restore into a new empty directory; retain the original database.")
	} else {
		add("store", "ready", "Current store format, integrity and relationships passed read-only verification.", "")
	}
	profiles := map[string]harness.KeyProfileView{}
	store, keysErr := keyStore()
	if keysErr == nil {
		report["keys_file"] = store.Path()
		var views []harness.KeyProfileView
		views, keysErr = store.List()
		keys := make([]map[string]any, 0, len(views))
		for _, view := range views {
			profiles[view.ID] = view
			key := map[string]any{"id": view.ID, "name": view.Name, "source": keySourceText(view), "last_check": view.LastCheck}
			if !harness.IsChannelKeyProtocol(view.Protocol) && configErr == nil {
				key["enabled"] = config.Providers[view.ID]
			}
			keys = append(keys, key)
		}
		report["keys"] = keys
	}
	if keysErr != nil {
		detail := doctorError(keysErr)
		report["keys_error"] = detail
		add("keys", "error", detail, "Check FREEAGENT_KEYS_FILE and file permissions. Retain the original keys file before correcting its format; doctor does not decrypt or test saved credentials.")
	} else {
		add("keys", "ready", "Key metadata is readable; credential values and connections were not tested.", "")
	}
	if configErr == nil && keysErr == nil {
		check := doctorSelection(config, profiles)
		add(check.ID, check.Status, check.Detail, check.Action)
	} else {
		add("model_selection", "skipped", "Model setup cannot be assessed until configuration and key metadata are readable.", "Fix the errors above, then rerun doctor.")
	}
	sandbox := harness.CheckSandbox(ctx)
	report["sandbox"] = sandbox
	needed := configErr == nil && config.Commands.Enabled && config.Commands.Execution != harness.CommandExecutionHostExplicit
	if sandbox.Status == "ready" {
		add("sandbox", "ready", sandbox.Detail, "")
	} else if needed {
		add("sandbox", "error", sandbox.Detail, "Linux: check bubblewrap and user namespaces. Windows: check AppContainer availability. Full/host is an explicit execution choice in settings; there is no automatic fallback.")
	} else {
		add("sandbox", "warning", sandbox.Detail, "The current configuration does not require this sandbox. Its absence does not prevent chat or explicit Full/host commands.")
	}
	if configErr == nil {
		detail, action := "command_run is disabled.", "Enable commands in Settings when needed, and select the intended space permission."
		status := "disabled"
		if config.Commands.Enabled {
			status, detail, action = "configured", "Command policy is enabled; each space and requester still applies its own permission.", "Use tools list for the effective tool view."
		}
		add("commands", status, detail, action)
		if config.PublicWeb.Enabled {
			add("public_web", "configured", "Public Web is configured; TLS, the reverse proxy and public reachability were not tested.", "Use public status and owner status locally; validate the Caddy configuration and inspect the service journal.")
		} else {
			add("public_web", "disabled", "Public Web is disabled. Local GUI/CLI access is independent.", "")
		}
	}
	if err := ctx.Err(); err != nil {
		add("diagnosis", "error", "Diagnosis was interrupted; completed checks remain in this report.", "Rerun doctor when the interruption is resolved.")
	}
	report["ok"], report["checks"] = ok, checks
	return report, checks, ok
}

func doctorSelection(config harness.Config, profiles map[string]harness.KeyProfileView) doctorCheck {
	check := doctorCheck{ID: "model_selection", Status: "warning"}
	selection := config.DefaultSelection
	if selection == "" {
		selection = config.DefaultModel
	}
	ids := []string{selection}
	if selection == "auto" {
		ids = append([]string(nil), config.Auto.Models...)
		if len(ids) == 0 {
			for id := range config.Models {
				ids = append(ids, id)
			}
		}
	}
	if selection == "" || len(ids) == 0 {
		check.Detail, check.Action = "No default model is configured.", "Add a Provider and model in Key management, then enable that Provider and choose a default model. CLI: key add, config model, key enable."
		return check
	}
	var unavailable []string
	for _, id := range ids {
		model, exists := config.Models[id]
		switch {
		case !exists:
			unavailable = append(unavailable, id+": model is missing")
		case !config.ProviderEnabled(model):
			unavailable = append(unavailable, id+": Provider is disabled")
		case model.Provider != "echo" && profiles[model.Key].ID == "":
			unavailable = append(unavailable, id+": Key profile is missing")
		}
	}
	if len(unavailable) != 0 {
		sort.Strings(unavailable)
		check.Detail, check.Action = strings.Join(unavailable, "; "), "Review Key management or key list; enable the intended Provider or select another configured model. Test the connection explicitly after setup."
		return check
	}
	check.Status, check.Detail = "configured", "Selected model metadata and Provider switches are configured; credentials and network requests remain untested."
	return check
}

// Raw decoding/SQLite errors may quote user content. Report an actionable
// category rather than copying configuration values or database rows.
func doctorError(err error) string {
	var syntax *json.SyntaxError
	var valueType *json.UnmarshalTypeError
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "A required file or directory is missing."
	case errors.Is(err, os.ErrPermission):
		return "A required file or directory is not accessible to this OS user."
	case errors.As(err, &syntax):
		return fmt.Sprintf("Invalid JSON near byte %d.", syntax.Offset)
	case errors.As(err, &valueType):
		return fmt.Sprintf("A JSON value has the wrong type near byte %d.", valueType.Offset)
	case errors.Is(err, harness.ErrStoreFormat):
		return "Database format or schema does not match this FreeAgent version."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "The check was interrupted before completion."
	default:
		return "Validation failed: check the current format, paths and file integrity."
	}
}

func writeDoctorText(writer io.Writer, data string, checks []doctorCheck) error {
	if _, err := fmt.Fprintf(writer, "FreeAgent doctor\nData: %s\nOffline diagnosis; no Provider request or saved-credential test.\n", data); err != nil {
		return err
	}
	for _, check := range checks {
		if _, err := fmt.Fprintf(writer, "\n[%s] %s: %s\n", check.Status, check.ID, check.Detail); err != nil {
			return err
		}
		if check.Action != "" {
			if _, err := fmt.Fprintln(writer, "  Next:", check.Action); err != nil {
				return err
			}
		}
	}
	return nil
}
