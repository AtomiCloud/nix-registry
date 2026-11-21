package gen

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"gopkg.in/yaml.v3"
)

// RegistrationID separates libraries even when their contract paths are identical.
func RegistrationID(c *contract.Contract, o Options) string {
	if o.ContractID != "" {
		return o.ContractID
	}
	return o.PackageID + ":" + ContractPath(c)
}

// PackageIdentity reads the nearest language manifest at or above out. An explicit
// package_id also supports repositories whose manifests are supplied at build time.
func PackageIdentity(root string, target contract.Target) (string, error) {
	id, _, err := packageIdentityLocation(root, target)
	return id, err
}

func packageIdentityLocation(root string, target contract.Target) (string, string, error) {
	if id := strings.TrimSpace(target.PackageID); id != "" {
		base, err := owningProject(root, target)
		return id, base, err
	}
	file, dir, err := packageManifest(root, target)
	if err != nil {
		return "", "", err
	}
	fail := func(err error) (string, string, error) {
		return "", "", fmt.Errorf("generate %s out: %q: package identity: %w (or set package_id:)", target.Lang, target.Out, err)
	}
	b, e := os.ReadFile(file)
	if e != nil {
		return fail(e)
	}
	name := filepath.Base(file)
	var id string
	switch target.Lang {
	case "go":
		match := regexp.MustCompile(`(?m)^\s*module\s+("[^"\n]*"|[^\s]+)`).FindSubmatch(b)
		if len(match) > 1 {
			id = string(match[1])
			if strings.HasPrefix(id, "\"") {
				id, e = strconv.Unquote(id)
			}
		}
	case "bun":
		var m struct {
			Name string `json:"name"`
		}
		e = json.Unmarshal(b, &m)
		id = m.Name
	case "dart":
		var m struct {
			Name string `yaml:"name"`
		}
		e = yaml.Unmarshal(b, &m)
		id = m.Name
	case "dotnet":
		var m struct {
			Groups []struct {
				PackageID    string `xml:"PackageId"`
				AssemblyName string `xml:"AssemblyName"`
			} `xml:"PropertyGroup"`
		}
		e = xml.Unmarshal(b, &m)
		for _, g := range m.Groups {
			if literalMSBuild(g.PackageID) != "" {
				id = literalMSBuild(g.PackageID)
				break
			}
		}
		if id == "" {
			for _, g := range m.Groups {
				if literalMSBuild(g.AssemblyName) != "" {
					id = literalMSBuild(g.AssemblyName)
					break
				}
			}
		}
		if id == "" {
			id = strings.TrimSuffix(name, ".csproj")
		}
	}
	if e != nil {
		return fail(fmt.Errorf("%s: %w", file, e))
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fail(fmt.Errorf("%s has no package identity", file))
	}
	return id, dir, nil
}

// owningProject locates a manifest without interpreting its identity properties.
// An override still works when a build supplies the manifest later.
func owningProject(root string, target contract.Target) (string, error) {
	_, dir, _ := packageManifest(root, target)
	if dir != "" {
		return dir, nil
	}
	if git := GitRoot(root); git != "" {
		return resolvedPath(git)
	}
	return resolvedPath(contract.ProjectRoot(root))
}

func literalMSBuild(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "$(") || strings.Contains(value, "@(") || strings.Contains(value, "%(") {
		return ""
	}
	return value
}

func packageManifest(root string, target contract.Target) (string, string, error) {
	start, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(target.Out)))
	if err != nil {
		return "", "", err
	}
	fail := func(err error) (string, string, error) {
		return "", "", fmt.Errorf("generate %s out: %q: package identity: %w (or set package_id:)", target.Lang, target.Out, err)
	}
	start, err = resolvedPath(start)
	if err != nil {
		return fail(err)
	}
	boundary := GitRoot(root)
	home := ""
	if boundary == "" {
		home = contract.HomeBoundary(start)
	}
	if boundary != "" {
		boundary, err = resolvedPath(boundary)
		if err != nil {
			return fail(err)
		}
		if !within(boundary, start) {
			return fail(fmt.Errorf("target is outside Git boundary %s", boundary))
		}
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		if dir == home && dir != start {
			break
		}
		name := ""
		switch target.Lang {
		case "go":
			name = "go.mod"
		case "bun":
			name = "package.json"
		case "dart":
			name = "pubspec.yaml"
		case "dotnet":
			matches, e := filepath.Glob(filepath.Join(dir, "*.csproj"))
			if e != nil {
				return fail(e)
			}
			if len(matches) > 1 {
				_, _, identityErr := fail(fmt.Errorf("multiple .csproj manifests in %s", dir))
				return "", dir, identityErr
			}
			if len(matches) == 1 {
				name = filepath.Base(matches[0])
			}
		}
		if name != "" {
			file := filepath.Join(dir, name)
			_, e := os.Stat(file)
			if e == nil {
				return file, dir, nil
			}
			if !os.IsNotExist(e) {
				return fail(e)
			}
		}
		if dir == boundary || dir == home || filepath.Dir(dir) == dir {
			break
		}
	}
	return fail(fmt.Errorf("no manifest from %s through project boundary %s", start, boundary))
}

// RegistrationIdentity uses the owning manifest folder with or without Git.
// An explicit package_id changes the identity value, not its project boundary.
func RegistrationIdentity(obsDir string, target contract.Target) (string, error) {
	root := Root(obsDir)
	id, base, err := packageIdentityLocation(root, target)
	if err != nil {
		return "", err
	}
	base, err = resolvedPath(base)
	if err != nil {
		return "", err
	}
	source, err := resolvedPath(filepath.Join(obsDir, "contract.yaml"))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(base, source)
	if err != nil {
		return "", err
	}
	return id + ":" + filepath.ToSlash(relative), nil
}
