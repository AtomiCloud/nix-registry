package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func stateFile(cfg Config) string {
	dir := cfg.StateDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir = filepath.Join(base, "obs")
	}
	return filepath.Join(dir, "sandbox-"+cfg.Project+".json")
}

// ReadState returns what `up` recorded for the project, or nil.
func ReadState(cfg Config) *Info { cfg.defaults(); return readState(cfg) }

func readState(cfg Config) *Info {
	b, err := os.ReadFile(stateFile(cfg))
	if err != nil {
		return nil
	}
	var i Info
	if json.Unmarshal(b, &i) != nil || i.Grafana == "" {
		return nil
	}
	return &i
}

// writeState replaces the state file in one step (a temporary file, then a rename), so a reader or a crash
// never sees half a file. A sandbox works without its state (it only remembers the seed window), so most
// callers ignore the error; the mark that a seed load began must not (see loadSeed).
func writeState(cfg Config, i *Info) error {
	f := stateFile(cfg)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f), filepath.Base(f)+".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), f); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func removeState(cfg Config) { _ = os.Remove(stateFile(cfg)) }
