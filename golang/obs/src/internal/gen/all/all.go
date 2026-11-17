// Package all imports every language generator, so each registers itself (from init()) with
// package gen. It lives in its own package because the generators import gen: a registry file
// inside gen would be an import cycle. cmd_gen.go imports this package for its side effect.
//
// Add exactly one import line per language below (go, dotnet, dart, bun).
package all

import (
	_ "github.com/AtomiCloud/obs/internal/gen/bun"
	_ "github.com/AtomiCloud/obs/internal/gen/dart"
	_ "github.com/AtomiCloud/obs/internal/gen/dotnet"
	_ "github.com/AtomiCloud/obs/internal/gen/golang"
)
