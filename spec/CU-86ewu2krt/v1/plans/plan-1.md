# Plan 1: bunWrapper Infrastructure

**Depends on**: None
**Estimated complexity**: Medium

## Objective

Create the bunWrapper package type infrastructure including trivialBuilders, documentation, and skill.

## Steps

### Step 1: Create bunWrapper Directory Structure

Create the following files:

**bunWrapper/default.nix:**

```nix
{ nixpkgs, bun }:
let trivialBuilders = import ./trivialBuilders.nix { inherit nixpkgs bun; }; in
{
  md-mermaid-lint = import ./md-mermaid-lint { inherit nixpkgs bun trivialBuilders; };
}
```

### Step 2: Create trivialBuilders.nix

**bunWrapper/trivialBuilders.nix:**

```nix
{ nixpkgs, bun }:

{
  writeBunScriptBin = { name, version, src, buildInputs ? [] }:
    nixpkgs.stdenv.mkDerivation {
      inherit name version src;
      buildInputs = [ bun ] ++ buildInputs;

      installPhase = ''
        export HOME=$TMPDIR
        bun install --frozen-lockfile --production

        mkdir -p $out/bin
        cat > $out/bin/${name} << 'SCRIPT'
        #!/bin/sh
        exec ${bun}/bin/bun run $src/index.ts "$@"
        SCRIPT
        chmod +x $out/bin/${name}
      '';

      meta.mainProgram = name;
    };
}
```

### Step 3: Update root default.nix

Add bunWrapper section to `default.nix`:

```nix
# Add after bin section
bun = import ./bunWrapper { inherit nixpkgs; bun = nixpkgs-unstable.bun; };

# Add to merge
// bun
```

### Step 4: Create Documentation

**docs/developer/packaging/BunWrapper.md:**

- When to use bunWrapper
- File structure
- Required fields
- Example templates
- CI integration steps
- Comparison with other package types

### Step 5: Create Skill

**.claude/skills/bun-wrapper/SKILL.md:**

```markdown
---
name: bun-wrapper
description: Add or upgrade Bun-based wrapper packages in the nix-registry
trigger: /bun-wrapper
---

# Bun Wrapper Package

Guide for working with Bun-based wrapper packages.

> **Note:** This project uses `pls` instead of `task` for running Taskfile commands.

See [BunWrapper Guide](../../docs/developer/packaging/BunWrapper.md) for complete documentation on:

- Adding new bunWrapper packages
- Using trivialBuilders.writeBunScriptBin
- CI integration
```

## Files to Create/Modify

| File                                     | Action |
| ---------------------------------------- | ------ |
| `bunWrapper/default.nix`                 | Create |
| `bunWrapper/trivialBuilders.nix`         | Create |
| `default.nix`                            | Modify |
| `docs/developer/packaging/BunWrapper.md` | Create |
| `.claude/skills/bun-wrapper/SKILL.md`    | Create |

## Verification

- `nix eval .#bun` should not error
- Documentation follows existing patterns (BinWrapper.md, ShellWrapper.md)
- Skill follows existing patterns (bin-wrapper, shell-wrapper)
