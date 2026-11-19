package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockProject makes setup and removal of one project exclusive: Up holds it from its state read to its last
// state write, Down while it removes. Without it a second `obs look` of the same project would read the
// incomplete-seed mark of a load that is still running and tear down the stores that load is writing to.
// The lock is a flock on a file next to the state file, so it is released when the process ends, however it
// ends. A busy project is refused at once (no waiting).
func lockProject(cfg Config) (func(), error) {
	name := stateFile(cfg) + ".lock"
	// No usable state folder (no writable cache dir, as in a build sandbox): there is no state file to read an
	// incomplete mark from, and a seed load stops before uploading because it cannot write that mark, so there
	// is nothing for the lock to protect. Run without it rather than refuse.
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return func() {}, nil
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, nil
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another obs look or obs sandbox up/down of project %s is already running (lock %s): wait for it to finish, or use another --project", cfg.Project, name)
		}
		return nil, fmt.Errorf("sandbox lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
