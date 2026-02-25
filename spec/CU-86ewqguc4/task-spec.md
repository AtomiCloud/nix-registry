# Task Specification: Add coderabbit-cli as a tool to nix-registry (CU-86ewqguc4)

## Source

- Ticket: CU-86ewqguc4
- System: ClickUp
- URL: https://app.clickup.com/t/86ewqguc4

## Skill to Use

Use the `/bin-wrapper` skill. Reference: `docs/developer/packaging/BinWrapper.md`

## Objective

Add coderabbit-cli (CodeRabbit's AI code review CLI tool) to the nix-registry as a binWrapper package using **Template 2: Binary from Vendor Downloads**, including CI verification.

## Implementation Steps

### Step 1: Create `binWrapper/coderabbit.nix`

Use Template 2 from the bin-wrapper guide with these specifics:

**Required Fields:**

| Field          | Value                                  |
| -------------- | -------------------------------------- |
| `pname`        | `"coderabbit"`                         |
| `version`      | Find latest from installer or releases |
| `sha256`       | Platform-specific hashes (4 platforms) |
| `src`          | `builtins.fetchurl { url = ...; }`     |
| `installPhase` | Copy to `$out/bin/coderabbit`          |
| `meta`         | Description, license, platforms        |

**Optional Fields (REQUIRED for raw binaries):**

| Field         | Value    |
| ------------- | -------- |
| `unpackPhase` | `"true"` |
| `buildPhase`  | `"true"` |

**Platform Mapping (from installer analysis):**

```nix
plat = {
  x86_64-linux = "linux_x64";
  aarch64-linux = "linux_arm64";
  x86_64-darwin = "macos_x64";
  aarch64-darwin = "macos_arm64";
}.${system} or throwSystem;
```

**Download URL Pattern:**

```
https://cli.coderabbit.ai/releases/${version}/${plat}/coderabbit
```

### Step 2: Get SHA256 Hashes

Run for each platform and copy the `"hash"` value:

```bash
nix store prefetch-file --json https://cli.coderabbit.ai/releases/<VERSION>/linux_x64/coderabbit
nix store prefetch-file --json https://cli.coderabbit.ai/releases/<VERSION>/linux_arm64/coderabbit
nix store prefetch-file --json https://cli.coderabbit.ai/releases/<VERSION>/macos_x64/coderabbit
nix store prefetch-file --json https://cli.coderabbit.ai/releases/<VERSION>/macos_arm64/coderabbit
```

### Step 3: Import in `default.nix`

Add to the `bin` section:

```nix
bin = rec {
  # ... existing packages ...
  coderabbit = import ./binWrapper/coderabbit.nix { inherit nixpkgs; };
};
```

### Step 4: Test

```bash
# Build test
nix build .#coderabbit

# Runtime test
nix shell .#coderabbit -c coderabbit --version
```

### Step 5: Add to CI Verification

Edit `.github/workflows/ci.yaml`:

**Step 5a: Add to nix shell command** (line ~38-64)

Add `.#coderabbit` after the last package:

```yaml
.#deadcode
.#coderabbit    # Add here
-c bash -c '
```

**Step 5b: Add verification command** (line ~65-87)

Add the version check after the last command:

```yaml
deadcode --version &&
coderabbit --version &&    # Add here
helmlint --version &&
```

**Verification Command Pattern:** Use `coderabbit --version` (Standard CLI pattern)

## Acceptance Criteria

- [ ] `binWrapper/coderabbit.nix` created using Template 2 pattern
- [ ] All 4 platforms defined in `plat` and `sha256` attributes
- [ ] Imported in `default.nix` under `bin` attribute set
- [ ] `nix build .#coderabbit` succeeds
- [ ] `nix shell .#coderabbit -c coderabbit --version` works
- [ ] Added to `.github/workflows/ci.yaml` nix shell command
- [ ] Added verification command `coderabbit --version` in CI bash script

## Definition of Done

- [ ] All acceptance criteria met
- [ ] Commit message follows convention: `new(coderabbit): add coderabbit-cli as binWrapper package [CU-86ewqguc4]`

## Out of Scope

- Adding the `cr` alias
- Adding to `nix/registry.nix` for dev shells

## Reference Files

- Template: `docs/developer/packaging/BinWrapper.md` → Template 2
- CI Guide: `docs/developer/packaging/BinWrapper.md` → CI Integration section
- Example: `binWrapper/codecov.nix` (similar vendor download pattern)
- CI File: `.github/workflows/ci.yaml`
