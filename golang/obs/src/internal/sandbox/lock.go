package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFile is where the project lock lives. By default it is one fixed machine-wide place per project
// (/tmp/obs-sandbox-locks, read from no environment variable: not TMPDIR, not the cache folder), so two
// invocations that address the same container runtime and project share it whatever their environment. An explicit StateDir (tests) keeps
// its lock beside its state, so isolated runs do not contend.
// lockRoot is shared by every user: it is made world-writable with the sticky bit (like /tmp), so any user
// can create the lock of another project and none can remove another user's lock file.
var lockRoot = "/tmp/obs-sandbox-locks" // a var only so a test can point it at a temp folder

func lockFile(cfg Config) string {
	if cfg.StateDir != "" {
		return filepath.Join(cfg.StateDir, "sandbox-"+cfg.Project+".lock")
	}
	return filepath.Join(lockRoot, "sandbox-"+cfg.Project+".lock")
}

// lockProject makes setup and removal of one project exclusive: Up holds it from before its first state read
// to its last state write, Down while it removes. Without it a second `obs look` of the same project would read
// the incomplete-seed mark of a load that is still running and tear down the stores that load is writing to.
// It is a flock, released when the process ends however it ends. It fails closed: a busy project, or a lock
// that cannot be taken at all, is refused before any container is touched (no waiting, no running unlocked).
func lockProject(cfg Config) (func(), error) {
	name := lockFile(cfg)
	if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		return nil, fmt.Errorf("sandbox lock %s: %w (obs touches no container without it)", name, err)
	}
	if filepath.Dir(name) == lockRoot {
		_ = os.Chmod(lockRoot, os.ModeSticky|0o777) // undo the umask; fails harmlessly when another user owns it
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		// flock works on a read-only descriptor too (a lock file another user created).
		ro, rerr := os.Open(name)
		if rerr != nil {
			return nil, fmt.Errorf("sandbox lock %s: %w (obs touches no container without it)", name, err)
		}
		f = ro
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another obs look or obs sandbox up/down of project %s is already running (lock %s): wait for it to finish, or use another --project", cfg.Project, name)
		}
		return nil, fmt.Errorf("sandbox lock %s: %w (obs touches no container without it)", name, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
