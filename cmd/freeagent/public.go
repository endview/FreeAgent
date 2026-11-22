package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/user"
	"runtime"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

func runPublic(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("public <enable|status|configure|disable|password|clear-locks> [--data-dir DIR]")
	}
	action := args[0]
	flags := flag.NewFlagSet("public "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "Owner data directory")
	policy := flags.String("policy-json", "", "complete public Web policy (contains no credentials)")
	origin := flags.String("origin", "", "HTTPS origin for public enable")
	socket := flags.String("socket", "", "dedicated /run Unix socket for public enable (keeps existing value when omitted)")
	group := flags.String("socket-group", "", "proxy group for public enable (keeps existing value when omitted)")
	ceiling := flags.String("max-permission", "", "public permission ceiling (new entrances default to ask)")
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
	if action == "configure" || action == "disable" || action == "enable" {
		var c harness.PublicWebConfig
		if action != "configure" {
			config, err := harness.LoadConfig(*data)
			if err != nil {
				return err
			}
			c = config.PublicWeb.Effective()
			switch action {
			case "disable":
				c.Enabled = false
			case "enable":
				if runtime.GOOS != "linux" {
					return errors.New("public enable: the restricted proxy entrance requires Linux")
				}
				if !publicweb.CredentialReady(*data) {
					return publicweb.ErrCredential
				}
				if *origin != "" {
					c.Origin = *origin
				}
				if *socket != "" {
					c.Socket = *socket
				}
				if *group != "" {
					c.SocketGroup = *group
				}
				if *ceiling != "" {
					c.MaxPermission = harness.PermissionMode(*ceiling)
				} else if config.PublicWeb.Effective().Origin == "" {
					c.MaxPermission = harness.PermissionAsk
				}
				if _, err := user.LookupGroup(c.SocketGroup); err != nil {
					return errors.New("public enable: prepare the dedicated proxy group and runtime directory first; see docs/PUBLIC_WEB.md")
				}
				c.Enabled = true
			}
		} else {
			if err := json.Unmarshal([]byte(*policy), &c); err != nil {
				return usageErr("public configure: --policy-json must contain a complete policy")
			}
		}
		if err := c.Validate(); err != nil {
			return err
		}
		err := runControl(ctx, []string{"plan", "--data-dir", *data, "--kind", harness.ControlSetPublicWeb, "--arguments", string(mustPublicJSON(c))}, stdin, stdout, stderr)
		if err == nil && action == "enable" {
			_, _ = fmt.Fprintln(stderr, "Public configuration saved. Check freeagent public status; if restart_required, restart the Owner and verify the HTTPS proxy.")
		}
		return err
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
