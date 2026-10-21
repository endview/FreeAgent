package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/endview/freeagent/internal/chatui"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

const oneShotLease = 15 * time.Minute

func runWeb(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runWebWithBrowser(ctx, args, stdin, stdout, stderr, openBrowser)
}

func runWebWithBrowser(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, browser func(string) error) error {
	var exitOnStdinClose bool
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--exit-on-stdin-close" {
			exitOnStdinClose = true
			continue
		}
		filtered = append(filtered, arg)
	}
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "current Harness data directory")
	port := flags.Int("port", 0, "loopback port; 0 chooses an available port")
	open := flags.Bool("open-browser", false, "open the local Chat in the default Windows browser")
	if err := parseFlags(flags, filtered); err != nil {
		return err
	}
	if flags.NArg() != 0 || *port < 0 || *port > 65535 {
		return usageErr("web: invalid port or arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	if exitOnStdinClose {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			cancel()
		}()
		ctx = runCtx
	}
	tasks, closeAll, err := harness.OpenTasks(ctx, *data, harness.Adapters{})
	if err != nil {
		return err
	}
	defer closeAll()
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	attachSecret, err := handoff.NewSecret()
	if err != nil {
		return err
	}
	chatSecret, err := handoff.NewSecret()
	if err != nil {
		return err
	}
	token := chatSecret
	ownerKind := "interactive"
	if exitOnStdinClose {
		ownerKind = "one_shot"
	}
	ownerInfo := handoff.EngineInfo{Product: "FreeAgent", Origin: origin,
		Workspace: tasks.Config().Workspace, Generation: attachSecret[:16], PID: os.Getpid(), OwnerKind: ownerKind}
	if exitOnStdinClose {
		ownerInfo.ExpiresAt = time.Now().UTC().Add(oneShotLease).Format(time.RFC3339Nano)
	}
	published, err := handoff.Publish(*data, ownerInfo, attachSecret, chatSecret)
	if err != nil {
		return err
	}
	defer published.Close()
	handler, err := chatui.NewServerWithAttach(ctx, tasks, origin, token, attachSecret)
	if err != nil {
		return err
	}
	handler.SetGeneration(published.Info.Generation)
	handler.SetOwnerInfo(published.Info)
	defer handler.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	address := origin + "/#key=" + token
	_, _ = fmt.Fprintln(stdout, address)
	_, _ = fmt.Fprintln(stderr, "Local single-Agent Chat. Keep this launch address private. Closing the browser does not stop tasks.")
	if *open {
		if err := browser(address); err != nil {
			_, _ = fmt.Fprintln(stderr, "The browser could not open. Open the launch address above manually.")
		}
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		handler.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-handler.StopRequested():
		handler.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
