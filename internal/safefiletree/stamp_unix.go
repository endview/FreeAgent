//go:build linux || darwin

package safefiletree

import "golang.org/x/sys/unix"

// x/sys/unix normalizes Stat_t change time to Ctim on both Linux and Darwin.
func unixChangeTime(stat unix.Stat_t) int64 {
	return stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec
}
