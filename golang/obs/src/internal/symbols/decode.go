package symbols

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Symbolizer is separate from storage, permitting object-storage backends and
// deterministic offline tests without executing platform tools.
type Symbolizer interface {
	Resolve(context.Context, []byte, uint64) (string, error)
}
type LLVM struct{ Executable string }

var ErrSymbolizerUnavailable = errors.New("llvm-symbolizer unavailable")

func (l LLVM) Resolve(ctx context.Context, data []byte, offset uint64) (string, error) {
	exe := l.Executable
	if exe == "" {
		exe = "llvm-symbolizer"
	}
	resolvedExe, err := exec.LookPath(exe)
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return "", ErrSymbolizerUnavailable
	}
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "obs-symbol-*.dbg")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, resolvedExe, "--inlining=false", "--demangle", "--obj="+f.Name(), fmt.Sprintf("0x%x", offset)).Output()
	if err != nil {
		return "", fmt.Errorf("llvm-symbolizer: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 || lines[0] == "??" || strings.HasPrefix(lines[1], "??:") {
		return "", nil
	}
	return lines[0] + " in " + lines[1], nil
}

var identityLine = regexp.MustCompile(`(?m)^\s*build-id:\s*([A-Fa-f0-9]+)\s*$`)
var nativeFrame = regexp.MustCompile(`^(\s*(?:at )?)([A-Za-z0-9_][A-Za-z0-9_.-]*)\+0x([A-Fa-f0-9]+)(.*)$`)
var jitFrame = regexp.MustCompile(`^(\s*(?:at )?)([A-Za-z0-9_][A-Za-z0-9_.-]*)!0x(06[A-Fa-f0-9]{6})\+0x([A-Fa-f0-9]+)\s+mvid:([A-Fa-f0-9-]+)\s*$`)

type Decoder struct {
	Store      Backend
	Symbolizer Symbolizer
	Notes      io.Writer
}

func (d Decoder) Decode(ctx context.Context, stack string) (string, error) {
	matches := identityLine.FindAllStringSubmatch(stack, -1)
	id := ""
	for _, m := range matches {
		if id != "" && !strings.EqualFold(id, m[1]) {
			return "", fmt.Errorf("stack has conflicting build-id values; decode cause stacks separately")
		}
		id = m[1]
	}
	lines := strings.Split(stack, "\n")
	unavailable := false
	cache := map[string][]byte{}
	load := func(key string) ([]byte, error) {
		if b, ok := cache[key]; ok {
			return b, nil
		}
		b, err := d.Store.Get(ctx, key)
		if errors.Is(err, fs.ErrNotExist) {
			cache[key] = nil
			return nil, nil
		}
		if err == nil {
			cache[key] = b
		}
		return b, err
	}
	for i, line := range lines {
		if m := jitFrame.FindStringSubmatch(line); m != nil {
			key, err := (Ref{Kind: "jit", ID: m[5], App: m[2]}).Key()
			if err != nil {
				return "", err
			}
			b, err := load(key)
			if err != nil {
				return "", err
			}
			if b == nil {
				continue
			}
			token, e := strconv.ParseUint(m[3], 16, 32)
			if e != nil {
				return "", e
			}
			off, e := strconv.ParseUint(m[4], 16, 32)
			if e != nil {
				return "", e
			}
			loc, e := PDBLocation(b, uint32(token), uint32(off))
			if e != nil {
				return "", fmt.Errorf("%s: %w", key, e)
			}
			if loc != "" {
				lines[i] = m[1] + m[2] + "!0x" + m[3] + " in " + loc
			}
			continue
		}
		m := nativeFrame.FindStringSubmatch(line)
		if m == nil || id == "" {
			continue
		}
		key, err := (Ref{Kind: "native", ID: id, App: m[2]}).Key()
		if err != nil {
			return "", err
		}
		b, err := load(key)
		if err != nil {
			return "", err
		}
		if b == nil {
			key, err = (Ref{Kind: "dart", ID: id, App: m[2]}).Key()
			if err != nil {
				return "", err
			}
			b, err = load(key)
			if err != nil {
				return "", err
			}
		}
		if b == nil {
			continue
		}
		if unavailable {
			continue
		}
		off, err := strconv.ParseUint(m[3], 16, 64)
		if err != nil {
			return "", fmt.Errorf("offset 0x%s: %w", m[3], err)
		}
		s := d.Symbolizer
		if s == nil {
			s = LLVM{}
		}
		resolved, err := s.Resolve(ctx, b, off)
		if errors.Is(err, ErrSymbolizerUnavailable) {
			unavailable = true
			if d.Notes != nil {
				fmt.Fprintln(d.Notes, "obs symbols: llvm-symbolizer is unavailable; native/Dart frames kept as is (set --symbolizer or OBS_SYMBOLIZER)")
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		if resolved != "" {
			lines[i] = m[1] + resolved + m[4]
		}
	}
	return strings.Join(lines, "\n"), nil
}
