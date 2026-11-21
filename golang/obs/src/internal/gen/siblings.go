package gen

import (
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
)

// TargetContractPaths keeps Git discovery shared with the other commands. A source
// export instead uses its topmost ancestor containing any project marker.
func TargetContractPaths(c *contract.Contract) ([]string, error) {
	dir := filepath.Dir(c.File)
	if GitRoot(dir) != "" {
		return contract.Discover(dir)
	}
	return contract.DiscoverProject(dir)
}

// ValidGenerateConfig complements Contract.Validate for generator-owned syntax.
// Generation ignores malformed siblings; obs check still diagnoses each one.
func ValidGenerateConfig(c *contract.Contract) bool {
	return len(generateConfigIssues(c)) == 0
}

func generateConfigIssues(c *contract.Contract) []string {
	var issues []string
	root := Root(filepath.Dir(c.File))
	for _, target := range c.Generate {
		if err := checkDotnetAssembly(root, target); err != nil {
			issues = append(issues, err.Error())
		}
		if err := CheckInterfaces(target.Lang, target.Interfaces); err != nil {
			issues = append(issues, err.Error())
		}
		if err := checkOut(root, GitRoot(root), target.Lang, target.Out); err != nil {
			issues = append(issues, err.Error())
		}
		if target.Lang == "dotnet" && target.Module != "" {
			if _, err := CanonicalDotnetNamespace(target.Module); err != nil {
				issues = append(issues, err.Error())
			}
		}
	}
	return issues
}

// targetContractNames names every participant from their shared owning root.
func targetContractNames(cs []*contract.Contract) map[*contract.Contract]string {
	shared := ""
	for _, c := range cs {
		root := GitRoot(filepath.Dir(c.File))
		if root == "" {
			root = topmostProjectRoot(filepath.Dir(c.File))
		}
		if shared == "" {
			shared = root
		}
		for !within(shared, root) && filepath.Dir(shared) != shared {
			shared = filepath.Dir(shared)
		}
	}
	names := map[*contract.Contract]string{}
	for _, c := range cs {
		source, err := resolvedPath(c.File)
		if err == nil {
			if relative, err := filepath.Rel(shared, source); err == nil {
				names[c] = filepath.ToSlash(relative)
				continue
			}
		}
		names[c] = ContractPath(c)
	}
	return names
}

// topmostProjectRoot is an export boundary, independent of language and identity.
func topmostProjectRoot(dir string) string { return contract.ProjectRoot(dir) }
