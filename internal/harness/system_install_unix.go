//go:build !windows

package harness

func isUnixSystemInstall(path string) bool {
	for _, root := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
		if within(root, path) {
			return true
		}
	}
	return false
}
