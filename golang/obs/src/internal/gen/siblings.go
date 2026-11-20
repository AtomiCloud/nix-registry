package gen

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/AtomiCloud/obs/internal/contract"
)

// TargetContractPaths keeps Git discovery shared with the other commands. A source
// export instead scopes generation siblings to the manifests owning its targets.
func TargetContractPaths(c *contract.Contract) ([]string, error) {
	dir := filepath.Dir(c.File)
	if GitRoot(dir) != "" {
		return contract.Discover(dir)
	}
	own, err := resolvedPath(c.File)
	if err != nil {
		return nil, err
	}
	roots := map[string]bool{}
	for _, target := range c.Generate {
		root, err := owningProject(Root(dir), target)
		if err == nil {
			roots[root] = true
		}
	}
	if len(roots) == 0 {
		root, err := resolvedPath(Root(dir))
		if err != nil {
			return nil, err
		}
		roots[root] = true
	}
	paths := map[string]bool{}
	for root := range roots {
		err := filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("generation contract discovery: %s: %w", file, err)
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".direnv", "node_modules", "vendor", ".dart_tool", ".obs-cache", "fixtures", "testdata":
					if file != root {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if file != own && d.Name() == "contract.yaml" && filepath.Base(filepath.Dir(file)) == "observability" && d.Type().IsRegular() {
				paths[file] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(paths))
	for file := range paths {
		result = append(result, file)
	}
	sort.Strings(result)
	return result, nil
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
			root = Root(filepath.Dir(c.File))
			if len(c.Generate) > 0 {
				if project, err := owningProject(root, c.Generate[0]); err == nil {
					root = project
				}
			}
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
