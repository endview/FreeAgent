package main

import (
	"fmt"
	"github.com/endview/freeagent-automation/internal/browser"
	"github.com/endview/freeagent-automation/internal/extwire"
	"os"
)

func main() {
	if err := extwire.Serve("cc.endview.browser", "0.1.1", &browser.Extension{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
