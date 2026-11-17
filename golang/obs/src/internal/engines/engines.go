// Package engines holds what the embedded store engines need once per process.
package engines

import (
	"flag"
	"os"
	"sync"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/logger"
	"golang.org/x/sys/unix"
)

var once sync.Once

// Init marks the global flag set parsed before the first engine call:
// VictoriaMetrics' lib/memory panics otherwise (v1.156.0 lib/memory/memory.go:43-46).
// It also caps the caches VictoriaMetrics sizes from the host's memory (60 % by
// default), so a CI runner keeps its memory budget, and quiets the engines' logger.
// obs never exposes VictoriaMetrics' global flags on its own command line.
func Init() {
	once.Do(func() {
		for k, v := range map[string]string{
			"memory.allowedBytes": "512MiB",
			"loggerLevel":         "ERROR",
		} {
			if flag.Lookup(k) != nil {
				_ = flag.Set(k, v)
			}
		}
		if !flag.CommandLine.Parsed() {
			_ = flag.CommandLine.Parse(nil)
		}
		logger.Init()
	})
}

// TmpBase is the directory the engines keep their storage in. OBS_TMPDIR wins;
// otherwise the system temp dir (TMPDIR in the nix sandbox).
func TmpBase() string {
	if d := os.Getenv("OBS_TMPDIR"); d != "" {
		return d
	}
	// A container often has only a 64 MiB /dev/shm. Use RAM storage only when
	// it has room for a CI seed; otherwise the normal temp directory is safer.
	var fs unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &fs); err == nil && uint64(fs.Bavail)*uint64(fs.Bsize) >= 1<<30 && unix.Access("/dev/shm", unix.W_OK) == nil {
		return "/dev/shm"
	}
	return ""
}
