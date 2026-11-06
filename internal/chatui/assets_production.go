//go:build !freeagentdev

package chatui

import "io/fs"

// Release builds never inspect development asset environment variables.
func developmentAssets(embedded fs.FS) fs.FS { return embedded }
