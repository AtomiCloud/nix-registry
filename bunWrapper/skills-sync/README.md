# skills-sync

Vendored-skill synchronisation and freshness, behind one entrypoint. `skills-sync` runs in
**any** consuming repository: every repository-specific fact comes from that repository's own
`skills-sync.yaml`, never from a constant baked into the tool.

```text
skills-sync sync
skills-sync check --tier <setup|pre-commit|ci>
skills-sync runtimes
skills-sync --help
skills-sync --version
```

There are **exactly three** commands. An unknown command exits `2` and lists the valid ones —
it is refused by name before any help handler can answer for it, so a command that does not
exist can never answer `--help` and read as real.

## The two halves

| Command            | Writes?                                                    | Runs at                       |
| ------------------ | ---------------------------------------------------------- | ----------------------------- |
| `sync`             | **yes** — it is the only thing that writes the vendor tree | setup and manual use **only** |
| `check --tier <t>` | **no** — read-only by construction                         | setup, pre-commit and CI      |

`sync` refuses when it detects a hook context and names the environment variable that gave it
away. The rule that no hook writes the vendor tree is enforced by the tool, not only by where
the tool is wired: a repository that adds `skills-sync sync` to a hook gets a refusal at the
first commit rather than a violation that survives until someone reads the hook file.

## The three tiers

The tiers differ on **exactly one** thing — what an unrestored dependency tree means. Nothing
else is tiered: drift, an invalid configuration, a vacuous subject set and a tool failure mean
the same thing in all three. A tier that behaved like its neighbour under that one condition
would be one tier wearing two names.

| Tier         | Dependencies not restored                                                                                                                             | Exit |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ---- |
| `setup`      | **refuses.** Setup owns restoring them, so an unrestored tree there is a broken setup.                                                                | `3`  |
| `pre-commit` | **skips**, and says in its own output that it is the warning tier and that CI is the guarantee. A commit must not require a restored dependency tree. | `0`  |
| `ci`         | **refuses.** This is the guarantee: never skipped, never conditional. CI is where the dependencies are in.                                            | `1`  |

## What `check` compares

Four subjects, because three of them have their own way of being silently wrong:

1. **content** — every file the resolved packages ship is in the vendor tree with the same
   bytes, and nothing else is.
2. **committed state** — every vendored file is tracked by git. A tree that is correct on disk
   and absent from git is regenerated locally and shipped to nobody.
3. **the manifest** — `manifest.json` describes the tree it sits in. It records only
   reproducible facts: a resolved source path lives under `$HOME` or `$GOPATH`, so writing one
   would make CI disagree with every developer's machine for a reason unrelated to skills.
4. **a subject at all** — a check with nothing to judge refuses instead of passing. Where a
   repository legitimately vendors nothing, that is a declaration: `requireSubjects: false`.

## Configuration

One file, read from the git work tree root: `skills-sync.yaml` (or `.yml`/`.json`; override
with `$SKILLS_SYNC_CONFIG`).

```yaml
schemaVersion: 1
runtime: bun # node | bun | dotnet | nuget | go | dart | pub | flutter
vendorDir: .claude/skills/vendor # optional
requireSubjects: true # optional
```

**Naming no runtime is OFF**, and off is a legitimate state: the tool is inert in a repository
that vendors no skills. That is what lets one generic, config-gated call site sit in every
template and do nothing wherever no runtime is named.

Off is not a hiding place, though. Two things are still loud:

- A configuration that names no runtime **while the vendor tree holds files** is refused.
  Deleting the config is not an off switch.
- A `runtime:` the tool does not recognise is **invalid** (exit `4`), never silently off.
  Treating an unrecognised name as "off" is exactly how a guarantee disappears from a
  repository that believes it has one.

## Adding a language

Adding a language changes **one** place: the new template's own `skills-sync.yaml`.

If the language is one of the built-in presets, that is a single `runtime:` line. If it is a
language `skills-sync` has never met, the same file may carry the whole resolver inline — and
`skills-sync`, workspace and shared are all untouched:

```yaml
schemaVersion: 1
resolver:
  name: zig
  declare:
    - file: build.zig.zon
      format: json # json | yaml | text
      maps: [dependencies]
      match: '^diene_'
  deps:
    requirePath: [zig-cache]
  resolve:
    strategy: json-file # path-template | json-file | json-command
    file: zig-packages.json
    listPath: installed
    nameKey: id
    dirKey: path
    subdir: skills
    vendorName: full # full | basename
```

`tests/run.sh` group G does exactly this and then fingerprints `src/` before and after, so the
"one place" claim is asserted rather than described.

## Exit codes

| Code | Meaning                                                             |
| ---- | ------------------------------------------------------------------- |
| `0`  | fresh, synchronised, or off                                         |
| `1`  | the vendored tree is stale, or a guarantee-tier precondition failed |
| `2`  | usage error, including the writer being invoked from a hook         |
| `3`  | a precondition is unsatisfied: dependencies are not restored        |
| `4`  | the configuration is invalid                                        |
| `5`  | skills-sync could not complete the inspection                       |

## Wiring a template

Three call sites, and none of them is bespoke — the same lines go in every template, and they
do nothing wherever no runtime is named.

```bash
# setup script, after dependencies are restored
skills-sync check --tier setup && skills-sync sync

# pre-commit hook — the READ-ONLY half only
skills-sync check --tier pre-commit

# CI — the guarantee
skills-sync check --tier ci
```

## Tests

```bash
./tests/run.sh                                                     # the working tree
SKILLS_SYNC="$(nix build --no-link --print-out-paths .#skills-sync)/bin/skills-sync" \
  ./tests/run.sh                                                   # the packaged binary
```

The battery is built around two rules, both learned from checks that turned out to be unable
to fail:

- **A positive control runs first in every group.** A mutation that turns a check red proves
  nothing if the check was already red, and an errored check reads exactly like a clean one.
- **Every mutation asserts a specific exit code and a specific reason**, never merely
  "non-zero". A check that refuses for the wrong reason will pass for the wrong reason later.

Every group therefore shows the check **green on a good subject and RED on a broken one**, and
group D shows the three tiers producing three distinct exit codes from one condition. The go
arm needs a real go toolchain; where it is absent the battery says so rather than dropping it
silently.
