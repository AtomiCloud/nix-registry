package gen

import (
	"fmt"
	"path"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
)

// DotnetNamespace preserves the root contract default and scopes feature contracts
// to the folder that owns their observability files. Explicit namespaces win.
func DotnetNamespace(c *contract.Contract, namespace string) string {
	if namespace != "" {
		return namespace
	}
	ident := func(value string) string {
		p := Pascal(Clean(value))
		if p == "" {
			return "Value"
		}
		if p[0] >= '0' && p[0] <= '9' {
			return "V" + p
		}
		return p
	}
	ns := ident(c.Service)
	dir := path.Dir(ContractPath(c))
	// App/App.csproj beside App/observability is a service root too. A
	// grouped contract below observability remains a feature contract.
	if filepath.Base(filepath.Dir(c.File)) == "observability" {
		projects, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(c.File)), "*.csproj"))
		if len(projects) > 0 {
			return ns + ".Signals"
		}
	}
	if dir != "." && dir != "observability" {
		feature := path.Base(dir)
		if feature == "observability" {
			feature = path.Base(path.Dir(dir))
		}
		ns += "." + ident(feature)
	}
	return ns + ".Signals"
}

// TargetClashes checks the resolved destinations of all contracts, independently
// of which feature is being generated. No manifest reads or generation is needed.
func TargetClashes(cs []*contract.Contract) []string {
	type owner struct {
		file, lang string
		contract   *contract.Contract
	}
	outputs := map[string]owner{}
	namespaces := map[string]owner{}
	var issues []string
	for _, c := range cs {
		for index, target := range c.Generate {
			current := owner{ContractPath(c), target.Lang, c}
			destination := resolve(filepath.Join(Root(filepath.Dir(c.File)), filepath.FromSlash(target.Out)))
			if previous, exists := outputs[destination]; exists && previous.contract != c {
				issues = append(issues, fmt.Sprintf("[generate-out-clash] contract %s (%s) and contract %s (%s) resolve to the same out: folder %s", previous.file, previous.lang, current.file, current.lang, contract.RelPath(filepath.Join(destination, "."))))
			} else {
				outputs[destination] = current
			}
			if target.Lang == "dotnet" {
				ns, err := CanonicalDotnetNamespace(DotnetNamespace(c, target.Module))
				if err != nil {
					issues = append(issues, fmt.Sprintf("[generate-namespace] contract %s generate[%d].%v", current.file, index, err))
					continue
				}
				if previous, exists := namespaces[ns]; exists && previous.contract != c {
					issues = append(issues, fmt.Sprintf("[generate-namespace-clash] contract %s and contract %s resolve to the same .NET namespace %s", previous.file, current.file, ns))
				} else {
					namespaces[ns] = current
				}
			}
		}
	}
	return issues
}
