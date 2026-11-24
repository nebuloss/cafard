//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// sameOwner gives the file at path the owner and group of the file at like,
// when this process runs as root: `cafard-server passwd`, run as root, replaces the
// configuration with a new file, which would otherwise be root's, and the
// service, running as its own user, could no longer read it. Measured on the
// test box: the service stopped at its next start, "permission denied".
func sameOwner(path, like string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	st, err := os.Stat(like)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(path, int(sys.Uid), int(sys.Gid))
}

// giveBack hands the database files to the owner of the state directory
// when this process runs as root.
//
// `cafard-server admin` is run as root on the box, beside a service that runs as
// its own user. SQLite creates the -wal and -shm files as whoever opens the
// database first; if that was root, the service could no longer open its own
// database after its next restart. The state directory's owner is the
// service's user, set by the init script, so that is who gets them.
func giveBack(stateDir string) {
	if os.Geteuid() != 0 {
		return
	}
	st, err := os.Stat(stateDir)
	if err != nil {
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid == 0 {
		return
	}
	for _, name := range []string{"cafard.db", "cafard.db-wal", "cafard.db-shm"} {
		os.Lchown(filepath.Join(stateDir, name), int(sys.Uid), int(sys.Gid))
	}
}
