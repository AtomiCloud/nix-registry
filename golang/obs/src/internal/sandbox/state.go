package sandbox

import (
	"encoding/json"
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

// writeState is best effort: a sandbox works without it, it only remembers the seed window.
func writeState(cfg Config, i *Info) {
	f := stateFile(cfg)
	if os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	b, _ := json.MarshalIndent(i, "", "  ")
	_ = os.WriteFile(f, b, 0o644)
}

func removeState(cfg Config) { _ = os.Remove(stateFile(cfg)) }
