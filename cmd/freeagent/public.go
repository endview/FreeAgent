package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

func runPublic(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("public <status|configure|disable|password|clear-locks> [--data-dir DIR]")
	}
	action := args[0]
	flags := flag.NewFlagSet("public "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	policy := flags.String("policy-json", "", "complete public Web policy (contains no credentials)")
	passwordStdin := flags.Bool("password-stdin", false, "read one password line from stdin; never pass passwords as arguments")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("public: unexpected argument")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	if action == "configure" || action == "disable" {
		var c harness.PublicWebConfig
		if action == "disable" {
			config, err := harness.LoadConfig(*data)
			if err != nil {
				return err
			}
			c = config.PublicWeb.Effective()
			c.Enabled = false
		} else {
			if err := json.Unmarshal([]byte(*policy), &c); err != nil {
				return usageErr("public configure: --policy-json must contain a complete policy")
			}
		}
		if err := c.Validate(); err != nil {
			return err
		}
		return runControl(ctx, []string{"plan", "--data-dir", *data, "--kind", harness.ControlSetPublicWeb, "--arguments", string(mustPublicJSON(c))}, stdin, stdout, stderr)
	}
	store, err := harness.OpenStore(ctx, *data)
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err := interactiveOwner(ctx, *data)
		if err != nil {
			return err
		}
		switch action {
		case "status":
			result, err := client.PublicSettings(ctx)
			if err != nil {
				return err
			}
			state, err := client.PublicRecovery(ctx, false)
			if err != nil {
				return err
			}
			result["runtime"] = state
			return writeJSON(stdout, result)
		case "clear-locks":
			result, err := client.PublicRecovery(ctx, true)
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		case "password":
			password, err := readPublicPassword(ctx, stdin, stderr, *passwordStdin)
			if err != nil {
				return err
			}
			defer clear(password)
			if err := client.PublicPassword(ctx, password); err != nil {
				return err
			}
			return writeJSON(stdout, map[string]bool{"saved": true, "sessions_revoked": true})
		default:
			return usageErr("public: unknown command")
		}
	}
	if err != nil {
		return err
	}
	defer store.Close()
	switch action {
	case "status":
		config, err := harness.LoadConfig(*data)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"config": config.PublicWeb.Effective(), "credential_ready": publicweb.CredentialReady(*data), "listener_active": false})
	case "clear-locks":
		return writeJSON(stdout, map[string]bool{"cleared": true, "owner_running": false})
	case "password":
		password, err := readPublicPassword(ctx, stdin, stderr, *passwordStdin)
		if err != nil {
			return err
		}
		defer clear(password)
		if err := publicweb.SetPassword(*data, password); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]bool{"saved": true})
	default:
		return usageErr("public: unknown command")
	}
}
func mustPublicJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
func readPublicPassword(ctx context.Context, stdin io.Reader, stderr io.Writer, fromStdin bool) ([]byte, error) {
	if fromStdin {
		return readKeyLine(stdin)
	}
	_, _ = fmt.Fprintln(stderr, "Administrator password (12–1024 bytes).")
	first, err := promptSecret(ctx, stdin, stderr, "Password (input hidden): ")
	if err != nil {
		return nil, err
	}
	second, err := promptSecret(ctx, stdin, stderr, "Repeat password (input hidden): ")
	defer clear(second)
	if err != nil || !bytes.Equal(first, second) {
		clear(first)
		return nil, errors.New("passwords do not match")
	}
	return first, nil
}
