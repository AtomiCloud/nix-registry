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
	return o.PackageID + ":" + ContractPath(c)
}

// PackageIdentity reads the nearest language manifest at or above out. An explicit
// package_id also supports repositories whose manifests are supplied at build time.
func PackageIdentity(root string, target contract.Target) (string, error) {
	if id := strings.TrimSpace(target.PackageID); id != "" {
		return id, nil
	}
	start, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(target.Out)))
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("generate %s out: %q: package identity: %w (or set package_id:)", target.Lang, target.Out, err)
	}
	boundary := GitRoot(root)
	if boundary == "" {
		boundary, err = filepath.Abs(root)
		if err != nil {
			return fail(err)
		}
	}
	if !within(boundary, start) {
		return fail(fmt.Errorf("target is outside project boundary %s", boundary))
	}
	for dir := start; ; dir = filepath.Dir(dir) {
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
				return fail(fmt.Errorf("multiple .csproj manifests in %s", dir))
			}
			if len(matches) == 1 {
				name = filepath.Base(matches[0])
			}
		}
		if name != "" {
			file := filepath.Join(dir, name)
			b, e := os.ReadFile(file)
			if e == nil {
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
						if g.PackageID != "" {
							id = g.PackageID
							break
						}
					}
					if id == "" {
						for _, g := range m.Groups {
							if g.AssemblyName != "" {
								id = g.AssemblyName
								break
							}
						}
					}
					if id == "" {
						id = strings.TrimSuffix(name, ".csproj")
					}
					if strings.Contains(id, "$(") {
						e = fmt.Errorf("%s uses an unevaluated MSBuild property", file)
					}
				}
				if e != nil {
					return fail(fmt.Errorf("%s: %w", file, e))
				}
				id = strings.TrimSpace(id)
				if id == "" {
					return fail(fmt.Errorf("%s has no package identity", file))
				}
				return id, nil
			}
			if !os.IsNotExist(e) {
				return fail(e)
			}
		}
		if dir == boundary || filepath.Dir(dir) == dir {
			break
		}
	}
	return fail(fmt.Errorf("no manifest from %s through project boundary %s", start, boundary))
}
