# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

AtomiCloud Nix Registry is a collection of custom Nix packages and derivations. The repository provides packages across multiple language ecosystems (Node.js, Python, Go, Rust, .NET, Shell) packaged for Nix/NixOS.

## Architecture

### Flake Structure

The repository uses Nix flakes with the following input sources:

- **nixpkgs-2505**: Primary package set (NixOS 25.05)
- **nixpkgs-unstable**: Unstable packages when needed
- **fenix**: Rust toolchain management
- **cyanprintpkgs**: Custom AtomiCloud package (sulfone.iridium)
- **atticpkgs**: Attic binary cache

### Package Organization

Packages are organized by language/type in directories:

- `binWrapper/`: Binary wrapper packages (mirrord, gardenio, atomiutils, infrautils, infralint, codecov)
- `shellWrapper/`: Shell script wrappers (pls, helmlint, dotnetlint)
- `node/22/`: Node.js 22 packages (uses node2nix)
- `python/`: Python packages (aws-export-credentials)
- `golang/`: Go packages (nix-share)
- `rust/`: Rust packages (toml-cli)
- `nuget/`: .NET packages

### Core Nix Files

- `flake.nix`: Main flake definition, system configuration
- `default.nix`: Package aggregation from all subdirectories
- `nix/registry.nix`: Curated registry of packages exposed to development shells
- `nix/env.nix`: Environment configuration
- `nix/fmt.nix`: Formatting configuration (nixpkgs-fmt, prettier, shfmt, actionlint)
- `nix/pre-commit.nix`: Pre-commit hooks (treefmt, infisical, gitlint, shellcheck)
- `nix/shells.nix`: Development shell definitions

### Build Flow

1. Language-specific directories (`node/`, `rust/`, etc.) contain package definitions
2. `default.nix` imports and combines all packages
3. `nix/registry.nix` selects packages for development environment
4. `flake.nix` orchestrates everything and provides outputs

## Common Commands

### Building

```bash
# Build all packages
task build
# Or directly
nix build

# Build specific package
nix build .#<package-name>
```

### Development Shell

```bash
# Enter development shell with pre-commit hooks
nix develop

# Or use direnv (if .envrc is configured)
direnv allow
```

### Formatting

```bash
# Format all files
nix fmt

# Formatter uses: nixpkgs-fmt, prettier, shfmt, actionlint
```

### Package Generation

```bash
# Generate Node.js package definitions (Node 22)
task gen:node:22

# Calculate Go module SHA
task gen:go:sha -- <nix-identifier> <path-to-nix-file>

# Calculate Go vendor SHA
task gen:go:vendor:sha -- <nix-identifier> <path-to-nix-file>

# Generate Gem package files
task gen:gem -- <app-name>
```

## Commit Conventions

This project uses **conventional commits** with specific types and scopes:

### Key Commit Types

- `new(scope)`: Release a new package (minor bump)
- `update(scope)`: Update a package's version
  - `update(patch)`: Patch version update
  - `update(minor)`: Minor version update
  - `update(major)`: Major version update
- `fix`: Bug fixes in derivations or config (patch bump)
  - `fix(drv)`: Fixes in nix derivations
  - `fix(config)`: Fixes in configuration (no bump)
- `remove`: Remove a package (major bump)
- `config`: Repository configuration changes (no bump)
  - `config(nix)`: Nix shell changes
  - `config(fmt)`: Formatter changes
  - `config(lint)`: Linter changes
- `ci`: CI pipeline changes (no bump)
- `docs`: Documentation updates (no bump)
- `chore`: Miscellaneous changes (no bump)

### Special Scope

- `no-release`: Prevents automatic release

See `docs/developer/CommitConventions.md` for complete specification.

## Pre-commit Hooks

Enabled hooks (see `nix/pre-commit.nix`):

- **treefmt**: Auto-formatting (excludes Changelog/README/CommitConventions)
- **infisical**: Secret scanning (all files + staged files)
- **gitlint**: Commit message linting
- **shellcheck**: Shell script linting
- Automatic executable permission enforcement for shell scripts

## Adding New Packages

1. Create package definition in appropriate language directory
2. Import in `default.nix`
3. Optionally add to `nix/registry.nix` if needed in dev shells
4. Run `nix build .#<package-name>` to test
5. Commit with `new(<package-name>): <description>` format

## CI/CD

- Uses GitHub Actions workflows (`.github/workflows/`)
- Automated releases via semantic-release (`atomi_release.yaml`)
- Release configuration in `atomi_release.yaml`
- Changelog automatically generated in `Changelog.md`
