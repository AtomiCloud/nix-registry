package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs one container-runtime command (docker or podman) and returns stdout.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// ExecRunner runs the runtime binary.
type ExecRunner struct{ Bin string }

// Run runs `<bin> args...`. The error carries stderr.
func (r ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s %s: %s", r.Bin, strings.Join(args[:min(len(args), 2)], " "), msg)
	}
	return out.String(), nil
}

// DetectRuntime picks the container runtime: want (a flag), else $OBS_CONTAINER_RUNTIME,
// else docker, else podman. Only those two are accepted.
func DetectRuntime(want string) (string, error) {
	if want == "" {
		want = os.Getenv("OBS_CONTAINER_RUNTIME")
	}
	if want != "" {
		if want != "docker" && want != "podman" {
			return "", fmt.Errorf("container runtime %q: want docker or podman", want)
		}
		p, err := exec.LookPath(want)
		if err != nil {
			return "", fmt.Errorf("container runtime %q is not on PATH", want)
		}
		return p, nil
	}
	for _, b := range []string{"docker", "podman"} {
		if p, err := exec.LookPath(b); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no container runtime: install docker or podman")
}
