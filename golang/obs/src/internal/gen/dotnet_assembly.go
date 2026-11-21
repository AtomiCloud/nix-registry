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
	projects, err := contract.ProjectFiles(root, ".csproj")
	if err != nil {
		return err
	}
	generated := filepath.Join(root, filepath.FromSlash(target.Out), "Signals.g.cs")
	scope := GitRoot(root)
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
		return fmt.Errorf("generate dotnet project %s: %w", contract.RelPath(file), err)
	}
	props, err := dotnetBuildProps(file, scope)
	if err != nil {
		return err
	}
	project.Properties = append(props.Properties, project.Properties...)
	project.Items = append(props.Items, project.Items...)
	rel, err := filepath.Rel(filepath.Dir(file), generated)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(file), ".csproj")
	compiles := (project.SDK != "" || len(project.SDKs) > 0) && within(filepath.Dir(file), generated)
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
		if strings.EqualFold(strings.TrimSpace(props.DefaultItems), "false") || strings.EqualFold(strings.TrimSpace(props.DefaultCompile), "false") {
			compiles = false
		}
		if value := strings.TrimSpace(props.AssemblyName); value != "" {
			name = value
		}
	}
	for _, group := range project.Items {
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
	if !compiles {
		return nil
	}
	if uncertainName || literalMSBuild(name) == "" {
		return fmt.Errorf("generate dotnet out: %q: project %s has a computed or conditional AssemblyName %q; cannot verify the required Signals assembly without evaluating MSBuild; use a literal AssemblyName Signals", target.Out, contract.RelPath(file), name)
	}
	if name == "Signals" {
		return nil
	}
	return fmt.Errorf("generate dotnet out: %q: project %s may compile Signals.g.cs with AssemblyName %q; internal carrier constructors require assembly name Signals; compile generated files in a Signals project (exclude them from the app and reference that project)", target.Out, contract.RelPath(file), name)
}

// MSBuild automatically imports the nearest Directory.Build.props. Literal
// compile items there are relative to the importing project, as in MSBuild.
func dotnetBuildProps(project, scope string) (dotnetProject, error) {
	var props dotnetProject
	for dir := filepath.Dir(project); within(scope, dir); dir = filepath.Dir(dir) {
		file := filepath.Join(dir, "Directory.Build.props")
		body, err := os.ReadFile(file)
		if err == nil {
			if err := xml.Unmarshal(body, &props); err != nil {
				return props, fmt.Errorf("generate dotnet props %s: %w", contract.RelPath(file), err)
			}
			return props, nil
		}
		if !os.IsNotExist(err) {
			return props, fmt.Errorf("generate dotnet props %s: %w", contract.RelPath(file), err)
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
