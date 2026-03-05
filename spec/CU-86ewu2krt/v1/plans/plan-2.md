# Plan 2: md-mermaid-lint Package

**Depends on**: Plan 1 (bunWrapper infrastructure)
**Estimated complexity**: Medium

## Objective

Create the md-mermaid-lint package that validates Mermaid syntax in Markdown files, and integrate it into CI.

## Steps

### Step 1: Create Package Directory

**bunWrapper/md-mermaid-lint/default.nix:**

```nix
{ nixpkgs, bun, trivialBuilders }:

let
  version = "1.0.0";
in
trivialBuilders.writeBunScriptBin {
  name = "md-mermaid-lint";
  inherit version;
  src = ./.;
  buildInputs = [ ];  # Optional additional build dependencies
}
```

### Step 2: Create package.json

**bunWrapper/md-mermaid-lint/package.json:**

```json
{
  "name": "md-mermaid-lint",
  "version": "1.0.0",
  "type": "module",
  "bin": {
    "md-mermaid-lint": "./src/index.ts"
  },
  "dependencies": {
    "unified": "^11.0.0",
    "remark-parse": "^11.0.0",
    "unist-util-visit": "^5.0.0",
    "mermaid": "^11.0.0",
    "glob": "^11.0.0"
  }
}
```

### Step 3: Create CLI Implementation

**bunWrapper/md-mermaid-lint/src/index.ts:**

Key implementation details:

1. Parse CLI arguments (glob patterns)
2. Use glob to find matching files
3. For each file:
   - Read content
   - Parse with remark-parse
   - Visit 'code' nodes with lang 'mermaid' or 'mmd'
   - Extract mermaid code block
   - Validate with mermaid.parse()
4. Report results with file:line format
5. Exit 1 if any errors, 0 otherwise
6. Support --version and --help flags

### Step 4: Install Dependencies

```bash
cd bunWrapper/md-mermaid-lint
bun install
```

This generates `bun.lock`.

### Step 5: Update default.nix (root)

Add bunWrapper to root `default.nix`:

```nix
bun = import ./bunWrapper/default.nix { inherit nixpkgs bun; };
```

### Step 6: Add to CI

Update `.github/workflows/ci.yaml`:

- Add `.#md-mermaid-lint` to nix shell command
- Add `md-mermaid-lint --version` to verification commands

### Step 7: Add to Registry (Optional)

Update `nix/registry.nix` if md-mermaid-lint should be available in dev shells.

## Verification Commands

````bash
# Build
nix build .#md-mermaid-lint

# Test version
nix run .#md-mermaid-lint -- --version

# Test with sample markdown
echo '```mermaid
graph TD; A-->B
```' > /tmp/test.md
nix run .#md-mermaid-lint -- /tmp/test.md
````

## Expected Output

```text
✅ /tmp/test.md:1 - Valid mermaid diagram
```

## Files to Create/Modify

| File                                       | Action           |
| ------------------------------------------ | ---------------- |
| bunWrapper/md-mermaid-lint/default.nix     | Create           |
| bunWrapper/md-mermaid-lint/package.json    | Create           |
| bunWrapper/md-mermaid-lint/src/index.ts    | Create           |
| bunWrapper/md-mermaid-lint/bun.lock        | Generate         |
| default.nix                                | Modify (add bun) |
| .github/workflows/ci.yaml                  | Modify           |
| docs/developer/packaging/CIVerification.md | Modify           |
