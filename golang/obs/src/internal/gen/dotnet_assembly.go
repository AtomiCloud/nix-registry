package gen

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
)

type dotnetProject struct {
	SDK        string     `xml:"Sdk,attr"`
	SDKs       []struct{} `xml:"Sdk"`
	Properties []struct {
		Condition      string `xml:"Condition,attr"`
		AssemblyName   string `xml:"AssemblyName"`
		DefaultItems   string `xml:"EnableDefaultItems"`
		DefaultCompile string `xml:"EnableDefaultCompileItems"`
	} `xml:"PropertyGroup"`
	Items []struct {
		Condition string `xml:"Condition,attr"`
		Compile   []struct {
			Condition string `xml:"Condition,attr"`
			Include   string `xml:"Include,attr"`
			Exclude   string `xml:"Exclude,attr"`
			Remove    string `xml:"Remove,attr"`
		} `xml:"Compile"`
	} `xml:"ItemGroup"`
}

// checkDotnetAssembly preserves the internal-constructor boundary. A project
// excluded by Compile Remove is not the consumer: the documented Signals glob
// project may compile the generated file instead. This does not evaluate MSBuild.
func checkDotnetAssembly(root string, target contract.Target) error {
	if target.Lang != "dotnet" {
		return nil
	}
	// Pure emitters also accept synthetic contracts whose logical folders do
	// not exist. There is no compiler project to inspect for those inputs.
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	projects, err := contract.ProjectFiles(root, ".csproj", target.Scope)
	if err != nil {
		return err
	}
	generated := filepath.Join(root, filepath.FromSlash(target.Out), "Signals.g.cs")
	scope := targetScopeRoot(root, target)
	if scope == "" {
		scope = contract.ProjectRoot(root)
	}
	for _, file := range projects {
		if err := checkDotnetConsumer(file, generated, scope, target); err != nil {
			return err
		}
	}
	return nil
}

// The package identity manifest and the compiler project may be different. A
// sibling project can include this output through a literal Compile Include.
func checkDotnetConsumer(file, generated, scope string, target contract.Target) error {
	var project dotnetProject
	body, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("generate dotnet project %s: %w", file, err)
	}
	if err := xml.Unmarshal(body, &project); err != nil {
		return fmt.Errorf("generate dotnet project %s: %w", target.Scope.Rel(file), err)
	}
	props, err := dotnetBuildImport(file, scope, "Directory.Build.props", target.Scope)
	if err != nil {
		return err
	}
	targets, err := dotnetBuildImport(file, scope, "Directory.Build.targets", target.Scope)
	if err != nil {
		return err
	}
	project.Properties = append(props.Properties, project.Properties...)
	project.Properties = append(project.Properties, targets.Properties...)
	rel, err := filepath.Rel(filepath.Dir(file), generated)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(file), ".csproj")
	defaultCompiles := (project.SDK != "" || len(project.SDKs) > 0) && within(filepath.Dir(file), generated)
	defaultItems, defaultCompileItems := true, true
	compiles := false
	uncertainName := false
	for _, props := range project.Properties {
		if conditionFalse(props.Condition) {
			continue
		}
		if !conditionTrue(props.Condition) {
			if value := strings.TrimSpace(props.AssemblyName); value != "" && value != name {
				uncertainName = true
			}
			continue
		}
		for _, value := range []struct {
			text string
			to   *bool
		}{{props.DefaultItems, &defaultItems}, {props.DefaultCompile, &defaultCompileItems}} {
			switch strings.ToLower(strings.TrimSpace(value.text)) {
			case "true":
				*value.to = true
			case "false":
				*value.to = false
			}
		}
		if value := strings.TrimSpace(props.AssemblyName); value != "" {
			name = value
		}
	}
	defaultCompiles = defaultCompiles && defaultItems && defaultCompileItems
	applyItems := func(part dotnetProject) {
		for _, group := range part.Items {
			if conditionFalse(group.Condition) {
				continue
			}
			for _, item := range group.Compile {
				if conditionFalse(item.Condition) {
					continue
				}
				if msbuildMatch(item.Include, rel) && !msbuildMatch(item.Exclude, rel) {
					compiles = true
				}
				if conditionTrue(group.Condition) && conditionTrue(item.Condition) && msbuildMatch(item.Remove, rel) {
					compiles = false
				}
			}
		}
	}
	// Props item operations precede the SDK's default Compile glob. A Remove
	// there cannot remove an item that the SDK has not added yet. Project and
	// Directory.Build.targets operations run after those defaults.
	applyItems(props)
	compiles = compiles || defaultCompiles
	applyItems(project)
	applyItems(targets)
	if !compiles {
		return nil
	}
	if uncertainName || literalMSBuild(name) == "" {
		return fmt.Errorf("generate dotnet out: %q: project %s has a computed or conditional AssemblyName %q; cannot verify the required Signals assembly without evaluating MSBuild; use a literal AssemblyName Signals", target.Out, target.Scope.Rel(file), name)
	}
	if name == "Signals" {
		return nil
	}
	return fmt.Errorf("generate dotnet out: %q: project %s may compile Signals.g.cs with AssemblyName %q; internal carrier constructors require assembly name Signals; compile generated files in a Signals project (exclude them from the app and reference that project)", target.Out, target.Scope.Rel(file), name)
}

// MSBuild automatically imports the nearest Directory.Build.props/targets. Literal
// compile items there are relative to the importing project, as in MSBuild.
func dotnetBuildImport(project, scope, name string, source *contract.Scope) (dotnetProject, error) {
	var props dotnetProject
	for dir := filepath.Dir(project); within(scope, dir); dir = filepath.Dir(dir) {
		file := filepath.Join(dir, name)
		body, err := os.ReadFile(file)
		if err == nil {
			if err := xml.Unmarshal(body, &props); err != nil {
				return props, fmt.Errorf("generate dotnet import %s: %w", source.Rel(file), err)
			}
			return props, nil
		}
		if !os.IsNotExist(err) {
			return props, fmt.Errorf("generate dotnet import %s: %w", source.Rel(file), err)
		}
		if dir == scope || filepath.Dir(dir) == dir {
			break
		}
	}
	return props, nil
}

func conditionFalse(value string) bool { return strings.EqualFold(strings.TrimSpace(value), "false") }
func conditionTrue(value string) bool {
	return strings.TrimSpace(value) == "" || strings.EqualFold(strings.TrimSpace(value), "true")
}

func msbuildMatch(patterns, file string) bool {
	file = filepath.ToSlash(filepath.Clean(file))
	for _, pattern := range strings.Split(patterns, ";") {
		pattern = strings.TrimSpace(strings.ReplaceAll(pattern, `\`, "/"))
		if pattern == "" {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "./")
		expr := regexp.QuoteMeta(pattern)
		expr = strings.ReplaceAll(expr, `\*\*/`, `(?:.*/)?`)
		expr = strings.ReplaceAll(expr, `\*\*`, `.*`)
		expr = strings.ReplaceAll(expr, `\*`, `[^/]*`)
		expr = strings.ReplaceAll(expr, `\?`, `[^/]`)
		if regexp.MustCompile("^" + expr + "$").MatchString(file) {
			return true
		}
	}
	return false
}
