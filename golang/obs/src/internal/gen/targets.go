package gen

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/contract"
	"os"
	"path"
	"path/filepath"
)

func csharpIdentifier(value string) string {
	p := Pascal(Clean(value))
	if p == "" {
		return "Value"
	}
	if p[0] >= '0' && p[0] <= '9' {
		return "V" + p
	}
	return p
}
func adjacentDotnetProject(c *contract.Contract) bool {
	if filepath.Base(filepath.Dir(c.File)) != "observability" {
		return false
	}
	projects, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(c.File)), "*.csproj"))
	return len(projects) > 0
}
func hasDotnetTarget(c *contract.Contract) bool {
	for _, target := range c.Generate {
		if target.Lang == "dotnet" {
			return true
		}
	}
	return false
}
func dotnetRootCandidate(c *contract.Contract) bool {
	dir := path.Dir(dotnetContractPath(c))
	return dir == "." || dir == "observability" || adjacentDotnetProject(c)
}

func dotnetContractPath(c *contract.Contract) string {
	if GitRoot(filepath.Dir(c.File)) == "" {
		// Synthetic contracts keep their supplied logical paths.
		if _, err := os.Stat(c.File); err == nil {
			if source, err := resolvedPath(c.File); err == nil {
				if relative, err := filepath.Rel(topmostProjectRoot(filepath.Dir(c.File)), source); err == nil {
					return filepath.ToSlash(relative)
				}
			}
		}
	}
	return ContractPath(c)
}

// DotnetNamespace uses valid siblings to disambiguate contracts of one service.
// Commands diagnose discovery errors separately.
func DotnetNamespace(c *contract.Contract, namespace string) string {
	if namespace != "" {
		return namespace
	}
	cs := []*contract.Contract{c}
	if _, err := os.Stat(c.File); err == nil {
		found, _ := TargetContractPaths(c)
		for _, file := range found {
			other, err := contract.Load(file)
			if err == nil && len(other.Validate()) == 0 && ValidGenerateConfig(other) {
				cs = append(cs, other)
			}
		}
	}
	return DotnetNamespaceForContracts(c, namespace, cs)
}
func DotnetNamespaceForContracts(c *contract.Contract, namespace string, cs []*contract.Contract) string {
	if namespace != "" {
		return namespace
	}
	ns := csharpIdentifier(c.Service)
	dir := path.Dir(dotnetContractPath(c))
	contracts, candidates := 0, 0
	for _, other := range cs {
		if other != c && !ValidGenerateConfig(other) {
			continue
		}
		if other.Service == c.Service && (other == c || hasDotnetTarget(other)) {
			contracts++
			if dotnetRootCandidate(other) {
				candidates++
			}
		}
	}
	if contracts <= 1 || (candidates == 1 && dotnetRootCandidate(c)) {
		return ns + ".Signals"
	}
	feature := path.Base(dir)
	if feature == "observability" {
		feature = path.Base(path.Dir(dir))
	}
	if feature == "." {
		feature = "Root"
	}
	return ns + "." + csharpIdentifier(feature) + ".Signals"
}

// signalFilename is the fixed per-contract file emitted by each language. A test
// compares this list with each registered generator's actual outputs.
func signalFilename(lang string) string {
	return map[string]string{"go": "signals.go", "bun": "signals.ts", "dotnet": "Signals.g.cs", "dart": "signals.g.dart"}[lang]
}

// TargetClashes compares resolved folders and every full generated destination
// across contracts. A single contract retains shared-folder/different-file support.
func TargetClashes(cs []*contract.Contract) []string {
	return TargetClashesForLanguages(cs, nil, nil)
}

// TargetClashesForLanguages selects primary targets without dropping the full
// contract context needed to compute valid siblings' default namespaces.
func TargetClashesForLanguages(cs []*contract.Contract, primary *contract.Contract, langs []string) []string {
	want := map[string]bool{}
	for _, lang := range langs {
		want[lang] = true
	}
	names := targetContractNames(cs)
	type owner struct {
		file, lang string
		c          *contract.Contract
	}
	folders := map[string]owner{}
	files := map[string]owner{}
	namespaces := map[string]owner{}
	identities := map[string]owner{}
	reportedIDs := map[string]bool{}
	badFolders := map[string]bool{}
	var issues []string
	checkFile := func(c *contract.Contract, lang, name string) {
		current := owner{names[c], lang, c}
		destination, err := generatedDestination(name)
		if err != nil {
			issues = append(issues, fmt.Sprintf("[generate-file] contract %s: %v", current.file, err))
			return
		}
		if previous, exists := files[destination]; exists && previous.c != c {
			if !(lang != "" && previous.lang != "" && badFolders[filepath.Dir(destination)]) {
				issues = append(issues, fmt.Sprintf("[generate-file-clash] contract %s and contract %s resolve to the same generated file %s", previous.file, current.file, destination))
			}
		} else {
			files[destination] = current
		}
	}
	for _, c := range cs {
		for _, file := range []string{EventsFile, AggregationFile, KeepFile} {
			checkFile(c, "", filepath.Join(filepath.Dir(c.File), file))
		}
		for index, target := range c.Generate {
			if c == primary && len(want) > 0 && !want[target.Lang] {
				continue
			}
			current := owner{names[c], target.Lang, c}
			if err := checkDotnetAssembly(Root(filepath.Dir(c.File)), target); err != nil {
				issues = append(issues, fmt.Sprintf("[generate-assembly] contract %s: %v", current.file, err))
				continue
			}
			if err := CheckInterfaces(target.Lang, target.Interfaces); err != nil {
				issues = append(issues, fmt.Sprintf("[generate-interfaces] contract %s: %v", current.file, err))
				continue
			}
			if err := checkOut(Root(filepath.Dir(c.File)), GitRoot(filepath.Dir(c.File)), target.Lang, target.Out); err != nil {
				issues = append(issues, fmt.Sprintf("[generate-out] contract %s: %v", current.file, err))
				continue
			}
			if id, err := RegistrationIdentity(filepath.Dir(c.File), target); err == nil {
				if previous, exists := identities[id]; exists && previous.c != c {
					key := id + "\x00" + current.file
					if !reportedIDs[key] {
						issues = append(issues, fmt.Sprintf("[generate-id-clash] contract %s and contract %s resolve to the same registration id %q; use distinct package_id: values or owning projects", previous.file, current.file, id))
						reportedIDs[key] = true
					}
				} else {
					identities[id] = current
				}
			}
			written := filepath.Join(Root(filepath.Dir(c.File)), filepath.FromSlash(target.Out))
			destination, err := resolvedPath(written)
			if err != nil {
				issues = append(issues, fmt.Sprintf("[generate-out] contract %s out: %q: %v", current.file, target.Out, err))
			} else {
				if previous, exists := folders[destination]; exists && previous.c != c {
					issues = append(issues, fmt.Sprintf("[generate-out-clash] contract %s (%s) and contract %s (%s) resolve to the same out: folder %s", previous.file, previous.lang, current.file, current.lang, destination))
					badFolders[destination] = true
				} else {
					folders[destination] = current
				}
				if file := signalFilename(target.Lang); file != "" {
					checkFile(c, target.Lang, filepath.Join(written, file))
				}
			}
			if target.Lang == "dotnet" {
				ns, err := CanonicalDotnetNamespace(DotnetNamespaceForContracts(c, target.Module, cs))
				if err != nil {
					issues = append(issues, fmt.Sprintf("[generate-namespace] contract %s generate[%d].%v", current.file, index, err))
					continue
				}
				if previous, exists := namespaces[ns]; exists && previous.c != c {
					issues = append(issues, fmt.Sprintf("[generate-namespace-clash] contract %s and contract %s resolve to the same .NET namespace %s; set namespace: on one target", previous.file, current.file, ns))
				} else {
					namespaces[ns] = current
				}
			}
		}
	}
	return issues
}
