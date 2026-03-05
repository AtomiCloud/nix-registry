# Task Spec: Add md-mermaid-lint to nix-registry

**Ticket**: CU-86ewu2krt
**Version**: 1
**Status**: Draft

## Overview

Add `md-mermaid-lint` tool to the nix-registry under the Sulfoxide platform. This tool validates Mermaid diagram syntax within Markdown files. Additionally, introduce a new `bunWrapper` package type for Bun-based tools and create a corresponding skill.

## Background & Motivation

### Why md-mermaid-lint?

Mermaid diagrams are widely used in documentation. Invalid Mermaid syntax can break documentation rendering. A linter that validates Mermaid blocks in Markdown files catches errors early in CI/CD pipelines.

### Why bunWrapper?

The original `md-mermaid-lint` npm package has heavy dependencies (puppeteer, canvas) that are difficult to package in Nix. Instead, we'll create a custom implementation using:

- **Bun runtime** - Fast TypeScript/JavaScript execution
- **remark-parse** (8,765 stars) - Proper markdown AST parsing
- **unist-util-visit** (987 stars) - AST node traversal
- **mermaid** (86,468 stars) - Official library with `parse()` API for syntax validation

This approach:

- Uses high-star, well-maintained packages
- Avoids puppeteer/browser dependencies
- Provides pure syntax validation (no rendering)
- Is easy to package in Nix

## Acceptance Criteria

### AC1: bunWrapper Package Type

- [ ] Create `bunWrapper/` directory structure
- [ ] Create `bunWrapper/default.nix` that imports all bun wrapper packages
- [ ] Create `bunWrapper/trivialBuilders.nix` with `writeBunScriptBin` function
- [ ] Add bunWrapper section to `default.nix`
- [ ] Document the pattern in `docs/developer/packaging/BunWrapper.md`

### AC2: md-mermaid-lint Package

- [ ] Create `bunWrapper/md-mermaid-lint/` directory
- [ ] Create `bunWrapper/md-mermaid-lint/default.nix` using trivialBuilders
- [ ] Create `bunWrapper/md-mermaid-lint/src/index.ts` with:
  - Markdown parsing via remark-parse
  - Mermaid block extraction (`mermaid` and `mmd`)
  - Syntax validation via mermaid.parse()
  - CLI interface with glob support
  - `--version` flag for CI verification
- [ ] Create `bunWrapper/md-mermaid-lint/package.json` with dependencies
- [ ] Create `bunWrapper/md-mermaid-lint/bun.lockb` (via `bun install`)

### AC3: Skill Creation

- [ ] Create `.claude/skills/bun-wrapper/SKILL.md`
- [ ] Document when to use bunWrapper vs other package types
- [ ] Include template and examples
- [ ] Reference BunWrapper.md documentation

### AC4: CI Integration

- [ ] Add `.#md-mermaid-lint` to CI nix shell command
- [ ] Add `md-mermaid-lint --version` verification command
- [ ] Update `docs/developer/packaging/CIVerification.md`

### AC5: Registry Integration

- [ ] Add `md-mermaid-lint` to `nix/registry.nix` for dev shell availability

## Technical Design

### Directory Structure

```
bunWrapper/
├── default.nix              # Imports all bun wrapper packages
├── trivialBuilders.nix      # writeBunScriptBin function
└── md-mermaid-lint/
    ├── default.nix          # Package derivation
    ├── package.json         # Bun dependencies
    ├── bun.lockb            # Lock file
    └── src/
        └── index.ts         # CLI implementation
```

### Dependencies

```json
{
  "dependencies": {
    "unified": "^11.0.0",
    "remark-parse": "^11.0.0",
    "unist-util-visit": "^5.0.0",
    "mermaid": "^11.0.0",
    "glob": "^11.0.0"
  }
}
```

### CLI Usage

```bash
# Validate all markdown files
md-mermaid-lint "**/*.md"

# Validate specific files
md-mermaid-lint README.md docs/**/*.md

# Show version
md-mermaid-lint --version

# Show help
md-mermaid-lint --help
```

### Output Format

```
✅ README.md:10 - Valid mermaid diagram
✅ docs/architecture.md:45 - Valid mmd diagram
❌ docs/flow.md:20 - Syntax error: Unexpected token
```

### Nix Builder Pattern

```nix
# bunWrapper/trivialBuilders.nix
{ nixpkgs, bun }:

{
  writeBunScriptBin = { name, version, src, bunDeps ? [] }:
    nixpkgs.stdenv.mkDerivation {
      inherit name version src;
      buildInputs = [ bun ] ++ bunDeps;
      installPhase = ''
        bun install --frozen-lockfile
        mkdir -p $out/bin
        cat > $out/bin/${name} << 'EOF'
        #!/bin/sh
        ${bun}/bin/bun run ${src}/index.ts "$@"
        EOF
        chmod +x $out/bin/${name}
      '';
    };
}
```

## Package Type Comparison

| Type         | Use Case            | Runtime | Dependencies |
| ------------ | ------------------- | ------- | ------------ |
| binWrapper   | Pre-built binaries  | Native  | None         |
| shellWrapper | Shell scripts       | Bash    | Shell tools  |
| bunWrapper   | TypeScript/JS tools | Bun     | npm packages |
| node/22      | Node.js libraries   | Node    | node2nix     |

## Out of Scope

- Creating pre-built binaries for md-mermaid-lint
- Adding other bunWrapper packages (future work)
- Modifying existing package types

## Edge Cases

1. **No mermaid blocks found**: Exit 0, report "No mermaid blocks found"
2. **Invalid glob pattern**: Exit 1, report error
3. **File not found**: Exit 1, report error
4. **Mixed valid/invalid**: Exit 1 after reporting all errors

## Testing Strategy

1. Build verification: `nix build .#md-mermaid-lint`
2. Version check: `nix run .#md-mermaid-lint -- --version`
3. Validation test: Create test markdown with valid/invalid mermaid
4. CI integration: Add to `.github/workflows/ci.yaml`

## References

- [remark](https://github.com/remarkjs/remark) - 8,765 stars
- [mermaid](https://github.com/mermaid-js/mermaid) - 86,468 stars
- [mermaid-cli](https://github.com/mermaid-js/mermaid-cli) - 4,206 stars
- [Bun](https://bun.sh/) - JavaScript runtime
