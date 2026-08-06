# skills-sync

`skills-sync` vendors package-provided agent skills from a repository's declared dependencies.

## Commands

- `skills-sync sync` is the writer. If dependencies are missing or only partially restored, it refuses with exit `3`; otherwise it writes the complete vendor tree and exits `0`. It never stages files.
- `skills-sync sync --frozen` is the enforcer. If dependencies are missing or partial, it reports the skip and exits `0`. Otherwise it writes, then refuses with exit `1` if any file changed or the git index disagrees with the complete vendor tree. A repaired but unstaged tree continues to refuse. It never stages files.
- `skills-sync runtimes` lists the built-in runtime presets.

Both sync modes refuse with exit `1` when dependencies are available but no skill resolves and `requireSubjects` is true.

## Configuration

Configuration is read from the git worktree root. Set `$SKILLS_SYNC_CONFIG` to override the default `skills-sync.yaml` path.

```yaml
schemaVersion: 1
runtime: bun # node | bun | dotnet | nuget | go | dart | pub | flutter
vendorDir: .claude/skills/vendor
requireSubjects: true
```

An unknown runtime can define an inline `resolver` in the same file. Naming no runtime disables synchronization, provided the repository has no vendored skills or undeclared diene subject.

## Wiring

| Call site             | Command                     |
| --------------------- | --------------------------- |
| Pre-commit hook entry | `skills-sync sync --frozen` |
| Taskfile setup step   | `skills-sync sync`          |

## Exit codes

| Code | Meaning                                                    |
| ---- | ---------------------------------------------------------- |
| `0`  | synchronized, frozen-clean, skipped, or off                |
| `1`  | the configured vendored-state contract is violated         |
| `2`  | invalid CLI usage                                          |
| `3`  | bare sync cannot run because dependencies are not restored |
| `4`  | invalid configuration                                      |
| `5`  | inspection or writing failed                               |

## Tests

```bash
./tests/run.sh
SKILLS_SYNC="$(nix build --no-link --print-out-paths .#skills-sync)/bin/skills-sync" ./tests/run.sh
```
