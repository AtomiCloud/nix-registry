package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// lockDir is where the project locks live by default: the system's /tmp itself, already world-writable with
// the sticky bit on Linux and macOS, so obs creates no shared folder and changes no permissions. It is read
// from no environment variable (not TMPDIR, not the cache folder), so two invocations that address the same
// container runtime and project share the lock whatever their environment. A var only so a test can point
// it at a temp folder.
var lockDir = "/tmp"

// projectName is what a lock file name may hold: the characters Docker allows in the container names obs
// builds from the project, so a project can never name a path outside lockDir.
var projectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func lockFile(cfg Config) string {
	if cfg.StateDir != "" { // tests: an isolated run keeps its lock beside its state
		return filepath.Join(cfg.StateDir, "sandbox-"+cfg.Project+".lock")
	}
	return filepath.Join(lockDir, "obs-sandbox-"+cfg.Project+".lock")
}

// lockProject makes setup and removal of one project exclusive: Up holds it from before its first state read
// to its last state write, Down while it removes. Without it a second `obs look` of the same project would read
// the incomplete-seed mark of a load that is still running and tear down the stores that load is writing to.
// It is a flock, released when the process ends however it ends. It fails closed: a busy project, or a lock
// that cannot be taken safely, is refused before any container is touched (no waiting, no running unlocked).
// The lock file is opened without following a symlink and without blocking (a FIFO in its place cannot stall
// the open), and must be a regular file.
func lockProject(cfg Config) (func(), error) {
	if !projectName.MatchString(cfg.Project) {
		return nil, fmt.Errorf("sandbox project %q: use letters, digits, '_', '.' and '-' (starting with a letter or digit)", cfg.Project)
	}
	name := lockFile(cfg)
	if cfg.StateDir != "" {
		if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox lock %s: %w (obs touches no container without it)", name, err)
		}
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		// flock works on a read-only descriptor too (a lock file another user created).
		ro, rerr := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if rerr != nil {
			return nil, fmt.Errorf("sandbox lock %s: %w (obs touches no container without it)", name, err)
		}
		f = ro
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("sandbox lock %s: not a regular file (obs touches no container without it)", name)
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
