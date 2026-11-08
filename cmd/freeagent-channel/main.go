package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/endview/freeagent/internal/buildinfo"
	"github.com/endview/freeagent/internal/channelplugin"
	"github.com/endview/freeagent/internal/channelwire"
)

type bootstrapFlags []string

func (f *bootstrapFlags) String() string         { return "explicit connection bootstrap files" }
func (f *bootstrapFlags) Set(value string) error { *f = append(*f, value); return nil }

func main() {
	var paths bootstrapFlags
	var state string
	var version bool
	flag.Var(&paths, "bootstrap-file", "Core-created connection bootstrap; repeat for accounts on the same Core")
	flag.StringVar(&state, "state-dir", "", "private pull progress directory")
	flag.BoolVar(&version, "version", false, "show channel process version")
	flag.Parse()
	if version {
		fmt.Printf("FreeAgent channel %s protocol=%d\n", buildinfo.Version, channelwire.Version)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := channelplugin.RunAll(ctx, paths, state); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
