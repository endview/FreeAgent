package publicweb

import (
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func listenRestricted(c harness.PublicWebConfig) (net.Listener, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		return nil, errors.New("public Web: run the Owner as its dedicated non-root service user")
	}
	group, err := user.LookupGroup(c.SocketGroup)
	if err != nil {
		return nil, errors.New("public Web: dedicated proxy group is missing")
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(c.Socket)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return nil, errors.New("public Web: create the dedicated non-symlink runtime directory using systemd")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0007 != 0 {
		return nil, errors.New("public Web: runtime directory must be private to Owner and proxy group")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, errors.New("public Web: runtime directory must be owned by the service user")
	}
	if err := os.Chown(dir, -1, gid); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0750); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(c.Socket); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("public Web: refusing to replace an unexpected socket path")
		}
		if conn, err := net.DialTimeout("unix", c.Socket, 200*time.Millisecond); err == nil {
			conn.Close()
			return nil, errors.New("public Web: socket is already active")
		}
		if err := os.Remove(c.Socket); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", c.Socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(c.Socket, -1, gid); err != nil {
		listener.Close()
		return nil, err
	}
	if err := os.Chmod(c.Socket, 0660); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
