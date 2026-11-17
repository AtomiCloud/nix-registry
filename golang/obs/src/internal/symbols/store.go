// Package symbols stores immutable symbols by build identity and decodes stacks
// at read time. A backend can be implemented by an object store without changing
// the decoder. The shipped backend is a local directory.
package symbols

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Backend interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
}

type Directory struct{ Root string }

func (d Directory) filename(key string) (string, error) {
	if d.Root == "" || !fs.ValidPath(key) || strings.Contains(key, `\`) {
		return "", fmt.Errorf("invalid symbol key %q", key)
	}
	return filepath.Join(d.Root, filepath.FromSlash(key)), nil
}
func (d Directory) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := d.filename(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(f)
}
func (d Directory) Put(ctx context.Context, key string, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := d.filename(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f), ".symbols-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Link is atomic and never replaces a symbol from an existing build identity.
	if err = os.Link(tmp.Name(), f); errors.Is(err, fs.ErrExist) {
		old, e := os.ReadFile(f)
		if e == nil && bytes.Equal(old, b) {
			return nil
		}
		return fmt.Errorf("symbol key %s already exists with different content", key)
	}
	return err
}

type Ref struct{ Kind, ID, App string }

var component = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var buildID = regexp.MustCompile(`^[a-fA-F0-9]{2,128}$`)
var mvid = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func (r Ref) Key() (string, error) {
	if r.Kind != "dart" && !component.MatchString(r.App) {
		return "", fmt.Errorf("app=%q: want a module name without path separators", r.App)
	}
	id := strings.ToLower(r.ID)
	switch r.Kind {
	case "native":
		if buildID.MatchString(id) && len(id)%2 == 0 {
			return id + "/" + r.App + ".dbg", nil
		}
	case "jit":
		if mvid.MatchString(id) {
			return id + "/" + r.App + ".pdb", nil
		}
	case "dart":
		if buildID.MatchString(id) && len(id)%2 == 0 {
			return id + ".symbols", nil
		}
	default:
		return "", fmt.Errorf("kind=%q: want native, jit or dart", r.Kind)
	}
	return "", fmt.Errorf("%s build identity=%q is invalid", r.Kind, r.ID)
}
func Put(ctx context.Context, b Backend, r Ref, data, obfuscationMap []byte) (string, error) {
	key, err := r.Key()
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%s: empty symbol file", key)
	}
	if r.Kind == "jit" {
		if _, err = readPDB(data); err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
	}
	if len(obfuscationMap) > 0 && r.Kind != "dart" {
		return "", fmt.Errorf("obfuscation map is only valid for dart")
	}
	if len(obfuscationMap) > 0 && !json.Valid(obfuscationMap) {
		return "", fmt.Errorf("obfuscation map is not valid JSON")
	}
	if err = b.Put(ctx, key, data); err != nil {
		return "", err
	}
	if len(obfuscationMap) > 0 {
		err = b.Put(ctx, strings.ToLower(r.ID)+".obfuscation-map.json", obfuscationMap)
	}
	return key, err
}
