package gen

import (
	"fmt"
	"regexp"
)

var dotnetInterfaces = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
var dartInterfaces = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// CheckInterfaces is shared by the generators and target validation.
// Empty means the language's default dependency.
func CheckInterfaces(lang, value string) error {
	if value == "" {
		return nil
	}
	if lang == "dotnet" && !dotnetInterfaces.MatchString(value) {
		return fmt.Errorf("interfaces %q must be a C# namespace", value)
	}
	if lang == "dart" && !dartInterfaces.MatchString(value) {
		return fmt.Errorf("interfaces %q must be a Dart package name", value)
	}
	return nil
}
