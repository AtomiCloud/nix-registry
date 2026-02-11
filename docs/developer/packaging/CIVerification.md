# CI Verification for Nix Packages

This document explains how packages are verified in CI and how to add new packages to verification.

## How CI Verification Works

The CI pipeline verifies that all packages in the registry build and run correctly. This is done in the `build` job in `.github/workflows/ci.yaml`.

### Verification Process

1. **Build Phase**: The `nix shell` command loads all packages into the environment
2. **Execution Phase**: Each package must execute a verification command (typically `--version`)
3. **Success Criteria**: All commands must complete with exit code 0

### Currently Verified Packages

The following packages are verified in CI with their respective commands:

| Package                | Command                            | Notes                                       |
| ---------------------- | ---------------------------------- | ------------------------------------------- |
| sg                     | `sg --version`                     | ast-grep                                    |
| upstash                | `upstash --version`                | Upstash CLI                                 |
| action_docs            | `action-docs --version`            | GitHub Actions docs generator               |
| typescript_json_schema | `typescript-json-schema --version` | JSON schema generator                       |
| swagger_typescript_api | `swagger-typescript-api --version` | API client generator                        |
| dotnetsay              | `dotnetsay`                        | No --version flag available                 |
| dotnet-ef              | `dotnet-ef --version`              | Entity Framework CLI                        |
| mirrord                | `mirrord --version`                | Mirrord CLI                                 |
| pls                    | `pls --version`                    | Prettier + ESLint wrapper                   |
| toml-cli               | `toml --version`                   | TOML CLI tool                               |
| nix-share              | `nix-share`                        | No --version flag available                 |
| cyanprint              | `cyanprint --version`              | AtomiCloud template tool                    |
| worktrunk              | `wt --version`                     | Worktrunk CLI (binary is `wt`)              |
| gardenio               | `garden version`                   | Garden CLI (uses `version` not `--version`) |
| codecov                | `codecov --version`                | Codecov CLI                                 |
| dotnetlint             | `dotnetlint --version`             | .NET linting wrapper                        |
| dn-inspect             | `dn-inspect --version`             | .NET inspection tool                        |
| deadcode               | `deadcode --version`               | Dead code detector                          |
| helmlint               | `helmlint --version`               | Helm linting wrapper                        |
| attic                  | `attic --version`                  | Attic binary cache client                   |
| cliproxyapi            | `cli-proxy-api --help`             | Only has --help, no --version               |
| aws-export-credentials | `aws-export-credentials --version` | AWS credential exporter                     |
| infrautils             | (removed from CI)                  | -                                           |
| infralint              | (removed from CI)                  | -                                           |

## Adding a New Package to CI

When adding a new package to the registry, you must also add it to CI verification.

### Step 1: Add the Package to Nix Shell Command

Add `.#<package-name>` to the `nix shell` command in `.github/workflows/ci.yaml`:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#sg
    .#your-new-package    # Add your package here
    ...
```

### Step 2: Add the Verification Command

Add the verification command inside the bash script:

```yaml
    -c bash -c '
    sg --version &&
    your-new-package --version &&    # Add your command here
    ...
    '
```

### Step 3: Handle Special Cases

Some packages require special handling:

#### Packages Without `--version`

If the package doesn't support `--version`, try:

1. **`version` subcommand**: Some tools use `version` instead

   ```bash
   garden version
   ```

2. **`--help` flag**: If only help is available

   ```bash
   cli-proxy-api --help
   ```

3. **Just run the binary**: If nothing else works
   ```bash
   dotnetsay
   nix-share
   ```

#### Different Binary Names

If the package name differs from the binary name (e.g., `worktrunk` package provides `wt` binary):

```bash
wt --version  # worktrunk's binary is 'wt'
```

## Verification Command Patterns

| Package Type      | Command Pattern      | Example                    |
| ----------------- | -------------------- | -------------------------- |
| Standard CLI      | `<name> --version`   | `codecov --version`        |
| Subcommand-based  | `<name> version`     | `garden version`           |
| Help only         | `<name> --help`      | `cli-proxy-api --help`     |
| No output options | `<name>`             | `dotnetsay`                |
| Different binary  | `<binary> --version` | `wt --version` (worktrunk) |

## Testing Locally Before Pushing

Always test your verification command locally before pushing to CI.

### Quick Test

Run the exact command that CI will use:

```bash
nix shell .#<package> -c <package> --version
```

### Example Tests

```bash
# Test a standard package
nix shell .#codecov -c codecov --version

# Test a package with different binary name
nix shell .#worktrunk -c wt --version

# Test a package with version subcommand
nix shell .#gardenio -c garden version

# Test a package without --version
nix shell .#dotnetsay -c dotnetsay
```

### Test Multiple Packages

To test like CI does:

```bash
nix shell .#package1 .#package2 -c bash -c '
package1 --version &&
package2 --version &&
echo "All tests passed!"
'
```

## Example CI Configuration

Here is the current CI verification configuration from `.github/workflows/ci.yaml` (lines 37-87):

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#sg
    .#upstash
    .#action_docs
    .#typescript_json_schema
    .#swagger_typescript_api
    .#dotnetsay
    .#dotnet-ef
    .#mirrord
    .#pls
    .#toml-cli
    .#nix-share
    .#aws-export-credentials
    .#cyanprint
    .#worktrunk
    .#atomiutils
    .#gardenio
    .#infrautils
    .#infralint
    .#codecov
    .#dotnetlint
    .#dn-inspect
    .#helmlint
    .#attic
    .#cliproxyapi
    .#deadcode
    -c bash -c '
    sg --version &&
    upstash --version &&
    action-docs --version
    typescript-json-schema --version &&
    swagger-typescript-api --version &&
    dotnetsay &&
    dotnet-ef --version &&
    mirrord --version &&
    pls --version &&
    toml --version &&
    nix-share &&
    cyanprint --version &&
    wt --version &&
    garden version &&
    codecov --version &&
    dotnetlint --version &&
    dn-inspect --version &&
    deadcode --version &&
    helmlint --version &&
    attic --version &&
    cli-proxy-api --help &&
    echo "Done"'
```

## Troubleshooting

### Package Not Found in CI

**Symptom**: Error message like `error: flake '...' does not provide attribute 'packages.x86_64-linux.<package>'`

**Solutions**:

1. Check that the package is exported in `default.nix`
2. Verify the package name matches exactly (case-sensitive)
3. Ensure the package builds locally: `nix build .#<package>`

### Verification Command Fails

**Symptom**: Command returns non-zero exit code

**Solutions**:

1. Test the command locally first
2. Check if `--version` is supported by running `--help`
3. Try alternative commands:
   - `<name> version` (without dashes)
   - `<name> -V`
   - `<name> --help`
   - Just `<name>` (if no version flag exists)

### Platform Issues

**Symptom**: Build fails on specific architecture (amd64 vs arm64)

**Solutions**:

1. Check `meta.platforms` in the package definition
2. Ensure all dependencies are available for both platforms
3. Check if the upstream package supports all required platforms

### Chained Command Issues

**Symptom**: Commands fail due to previous command's output

**Solutions**:

1. Each command is chained with `&&` - if one fails, all subsequent fail
2. Check the CI logs to identify which specific command failed
3. Test each command individually

## Best Practices

1. **Always test locally** before pushing changes to CI
2. **Use `--version`** as the primary verification method
3. **Document special cases** - if a package needs special handling, note it
4. **Keep the list alphabetical** - makes it easier to find packages
5. **Match the order** - keep packages and their commands in the same order

## Related Documentation

- [CLAUDE.md](../../CLAUDE.md) - Repository overview
