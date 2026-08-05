# dlint

Seven repo-agnostic repository linters behind one entrypoint. `dlint` runs in **any**
consuming repository: every repository-specific fact comes from that repository's own
configuration file, never from a constant baked into the tool.

```text
dlint <check> [args]
dlint --help
dlint --version
```

There are **exactly seven** checks. There are no aliases, no hidden checks and no `all`.
An unknown check exits `2` and lists the valid ones.

| Check                                | Refuses when                                                                                                                                                                                                         |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `action-pins <trusted\|non-trusted>` | An action reference has no authored trust classification; a `trusted` action is not on a major tag (`v3`); a `non-trusted` action is not on an exact 40-hex SHA whose trailing comment names its tag.                |
| `exec-bits`                          | A tracked shell script is not executable, or is missing from the worktree.                                                                                                                                           |
| `ci-wiring`                          | A CI entrypoint a workflow names is missing or not executable; an orchestrator job does not call a repository-local reusable workflow; a reusable workflow calls no CI entrypoint; an orchestrator declares no jobs. |
| `skills-fresh`                       | The vendored tree moved in the worktree after its own regeneration command ran, or there is no tracked subject to judge at all.                                                                                      |
| `toolchain-smoke`                    | A required binary does not resolve in the named shell where the check is run.                                                                                                                                        |
| `no-custom-derivations`              | A declared nix file uses a custom derivation builder instead of staying a plain declarative list.                                                                                                                    |
| `workflow-policy`                    | A declared path in a declared workflow file does not hold its declared value, or is absent. One assertion is one caught fault.                                                                                       |

## Configuration

All seven checks read **one** file: `$DLINT_CONFIG`, or `./.dlint.json`. `dlint` is run
from the repository root and reads every path relative to it.

```json
{
  "schemaVersion": 1,
  "checks": {
    "action-pins": {
      "trustMap": "config/action-trust.json",
      "workflowsDir": ".github/workflows"
    },
    "exec-bits": {
      "globs": ["*.sh"]
    },
    "ci-wiring": {
      "workflowsDir": ".github/workflows",
      "entrypointPattern": "scripts/ci/[A-Za-z0-9._-]+[.]sh",
      "orchestrators": [".github/workflows/ci.yaml", ".github/workflows/cd.yaml", ".github/workflows/release.yaml"]
    },
    "skills-fresh": {
      "regenerate": "bash scripts/local/skills-sync.sh",
      "paths": [".claude/skills/vendor"],
      "ignore": [".claude/skills/vendor/.gitkeep"]
    },
    "toolchain-smoke": {
      "shell": "default",
      "binaries": ["bash", "git"]
    },
    "workflow-policy": {
      "assertions": [
        {
          "file": ".github/workflows/release.yaml",
          "path": ".concurrency.group",
          "equals": "release",
          "reason": "release concurrency group must be release"
        }
      ]
    }
  }
}
```

One mechanism, applied uniformly: a section per check, keyed by the check's own name.

| Key                                     | Required | Default             |
| --------------------------------------- | -------- | ------------------- |
| `action-pins.trustMap`                  | yes      | —                   |
| `action-pins.workflowsDir`              | no       | `.github/workflows` |
| `action-pins.requireSubjects`           | no       | `true`              |
| `exec-bits.globs`                       | no       | `["*.sh"]`          |
| `exec-bits.requireSubjects`             | no       | `true`              |
| `ci-wiring.entrypointPattern`           | yes      | —                   |
| `ci-wiring.orchestrators`               | yes      | —                   |
| `ci-wiring.workflowsDir`                | no       | `.github/workflows` |
| `skills-fresh.regenerate`               | yes      | —                   |
| `skills-fresh.paths`                    | yes      | —                   |
| `skills-fresh.ignore`                   | no       | `[]`                |
| `toolchain-smoke.shell`                 | yes      | —                   |
| `toolchain-smoke.binaries`              | yes      | —                   |
| `no-custom-derivations.paths`           | yes      | —                   |
| `no-custom-derivations.forbid`          | no       | dlint's vocabulary  |
| `no-custom-derivations.requireSubjects` | no       | `true`              |
| Key                                     | Required | Default             |
| -----------------------------           | -------- | ------------------- |
| `action-pins.trustMap`                  | yes      | —                   |
| `action-pins.workflowsDir`              | no       | `.github/workflows` |
| `action-pins.requireSubjects`           | no       | `true`              |
| `exec-bits.globs`                       | no       | `["*.sh"]`          |
| `exec-bits.requireSubjects`             | no       | `true`              |
| `ci-wiring.entrypointPattern`           | yes      | —                   |
| `ci-wiring.orchestrators`               | yes      | —                   |
| `ci-wiring.workflowsDir`                | no       | `.github/workflows` |
| `skills-fresh.regenerate`               | yes      | —                   |
| `skills-fresh.paths`                    | yes      | —                   |
| `skills-fresh.ignore`                   | no       | `[]`                |
| `toolchain-smoke.shell`                 | yes      | —                   |
| `toolchain-smoke.binaries`              | yes      | —                   |
| `workflow-policy.assertions`            | yes      | —                   |
| ` ↳ .file`                              | yes      | —                   |
| ` ↳ .path`                              | yes      | —                   |
| ` ↳ .equals`                            | yes      | — (may not be null) |
| ` ↳ .reason`                            | yes      | —                   |

Only facts that are conventions of a **tool** rather than of a repository have defaults:
`.github/workflows` is GitHub's, `*.sh` is the shell's. Everything that describes how a
particular repository is laid out must be declared, because a default there would be the
tool guessing.

The `action-pins` trust map is the same `schemaVersion: 1` shape the check has always
read:

```json
{
  "schemaVersion": 1,
  "actions": {
    "AtomiCloud/actions.setup-nix": "trusted",
    "upsidr/merge-gatekeeper": "non-trusted"
  }
}
```

### An absent subject is never a pass

A check with no section in `.checks` is an **error** (exit `3`), not a pass. To turn a
check off you have to say so, and the run says so back:

```json
{ "checks": { "exec-bits": false } }
```

```text
⏭️ dlint exec-bits is disabled by '"exec-bits": false' in '.dlint.json'
```

That keeps _not configured_ and _deliberately off_ two different observable states.
Collapsing them is how a loud refusal turns into a green check.

## Exit codes

| Code | Meaning                                                                                     |
| ---- | ------------------------------------------------------------------------------------------- |
| `0`  | Conforms, or the check is explicitly disabled.                                              |
| `1`  | The repository breaks the rule. The message names the file, the line and what was expected. |
| `2`  | Usage error: unknown check, missing or bad argument.                                        |
| `3`  | The configuration, or a subject it declares, is **absent**.                                 |
| `4`  | The configuration is present but invalid: bad schema, wrong type, empty list.               |
| `5`  | `dlint` could not complete the inspection (a scan or a git call failed).                    |

`3` and `4` are deliberately distinct from `1`, and `5` from both: a caller that cannot
tell "you have not configured me" from "you are broken" from "I could not look" will
retry the wrong thing.

## Toolchain smoke

`toolchain-smoke` is called from the shell named in its configuration; the
configuration makes that shell and its required commands reviewable, while the
check verifies every command resolves in that real invocation environment. It
does not inspect package declarations: a developer-installed binary must not
make a declaration-only check pass.

## No custom derivations

`no-custom-derivations` enforces **D7 RESOLVER-SHAPE**: template nix stays plain declarative
lists, and custom builds live in the registry.

## Workflow policy

`workflow-policy` replaces hand-written `yq | jq` validators. The policy is
repository-specific, so every assertion is declared: the file, the path, the expected value,
and the reason to print when it does not hold.

```json
{
  "checks": {
    "no-custom-derivations": {
      "paths": ["nix/packages.nix", "nix/pre-commit.nix", "nix/env.nix"]
    "workflow-policy": {
      "assertions": [
        {
          "file": ".github/workflows/release.yaml",
          "path": ".on.workflow_run.workflows",
          "equals": ["CI"],
          "reason": "release must trigger from CI"
        },
        {
          "file": ".github/workflows/release.yaml",
          "path": ".concurrency.group",
          "equals": "release",
          "reason": "release concurrency group must be release"
        },
        {
          "file": ".github/workflows/ci.yaml",
          "path": ".name",
          "equals": "CI",
          "reason": "the CI workflow name must be exactly CI"
        }
      ]
    }
  }
}
```

The reason it refuses, which the refusal itself states: templates compose through the
**cyanprint nix resolver**, which merges simple attribute lists. A custom derivation is not
resolver-mergeable, so complexity is hoisted to the registry rather than carried by a child.

### The vocabulary, and why it is printed

This is an **absence** check, and _an absence claim is only as wide as its word list_.
Redundancy across spellings of one word is not coverage across a concept: a template that
avoided `overrideAttrs` could still carry a custom build through `runCommand` or
`symlinkJoin`, and a sweep for the first would report a clean tree with a straight face.

So the default vocabulary is several **different words** for one concept, ordered most
specific first so a refusal names the builder actually written:

```text
overrideAttrs  overrideDerivation  mkDerivation  runCommand  buildEnv  symlinkJoin
writeShellApplication  writeShellScriptBin  writeScriptBin  writeTextFile  derivation
```

Members that are already substrings of another member are deliberately left out —
`stdenv.mkDerivation` is caught by `mkDerivation`, `runCommandLocal` by `runCommand` — and
there is an arm proving that rather than an assertion claiming it.

This vocabulary is a convention of the **Nix language**, not of any repository, which is why
it has a default at all: the same reason `*.sh` is `exec-bits`' one default. A repository may
widen or narrow it with `forbid`. An explicitly **empty** `forbid` is refused (`4`), because a
check that forbids nothing passes unconditionally.

**Every run prints the vocabulary it enumerated:**

```text
ℹ️ files inspected: 3 (nix/packages.nix nix/pre-commit.nix nix/env.nix)
ℹ️ vocabulary enumerated (11): overrideAttrs overrideDerivation mkDerivation …
✅ Template nix stays plain declarative lists
```

A green that does not state its scope is the shape that retires doubt it never earned. If you
narrow `forbid` to one builder, the green says `vocabulary enumerated (1)` and the reader can
see for themselves how much the pass is worth.

Mentions inside comments count. That is deliberate: the check reads the file as written, and a
template that needs to discuss these builders in prose belongs in `docs/`, not in the nix
file the rule is about.

A declared path that does not exist is `3`, not clean — deleting the file a rule is about must
never be how the rule gets satisfied.
**One assertion is one caught fault.** A single combined predicate over four fields refuses
without saying which field moved, so each is declared and reported separately. A refusal
names the reason, the path, the value it **found** and the value it expected.

Four properties matter, and each has its own arm:

- **The read is not a pipeline.** `yq … | jq -e …` takes its exit status from `jq`, so a `yq`
  that fails and writes to stderr can leave `jq` reading empty input and the whole thing
  exiting `0`. That is a real defect in the validators this check replaces. Here `yq` writes
  to a file, its own status is checked, and only then is the file read.
- **An unreadable subject is `5`, never a pass.** A workflow file that does not parse makes
  the policy UNKNOWN, and UNKNOWN is not FAILED and is certainly not conforming.
- **A declared file that does not exist is `3`, not `1`.** An absent subject is not a
  violation; conflating the two misreports why the gate is red.
- **`null` cannot be an expectation.** It is how this check reports an absent path, so
  allowing it as a value would make "the field is missing" and "the field is correctly null"
  the same answer. `equals: null` is a configuration error.

`reason` is required. A refusal that cannot name the policy it enforces is the kind of gate
that gets deleted for being unexplainable.

## Runtime

Every binary the checks call — `bash`, `coreutils`, `findutils`, `git`, `grep`, `sed`,
`jq`, `ripgrep`, `yq` — is in the derivation's runtime closure. Nothing is assumed to be
on the consumer's `PATH`. The packages are declared individually rather than through the
`atomiutils` bundle, because a bundle plus its own members collide inside a `buildEnv`.

`skills-fresh` is the one check that runs a command from the configuration
(`regenerate`), in a `bash -c` at the repository root. The consuming repository owns
regeneration; `dlint` owns "regenerate, then refuse if the tree moved".

## Non-vacuity

**No check passes without having inspected something.** A `✅` after zero subjects is the
failure mode this repository's history keeps producing, so every check refuses instead:

- `skills-fresh` refuses when zero tracked subjects survive the `ignore` filter.
- `ci-wiring` requires at least one declared orchestrator, requires each to exist and to
  declare at least one job, and requires every job's reusable workflow to name a CI
  entrypoint — so a green `ci-wiring` has necessarily resolved at least one entrypoint.
  Its non-vacuity is structural: there is no knob, because there is no legitimate
  zero-job orchestrator.
- `action-pins` refuses any reference it cannot classify, and refuses when the workflow
  directory holds no action reference at all.
- `exec-bits` refuses when no tracked file matches its globs.

The last two are the only checks with a legitimate zero-subject state — a repository may
genuinely have no shell scripts, or no third-party actions — so that state is a
**declaration**, not a default:

```json
{ "checks": { "exec-bits": { "requireSubjects": false } } }
```

Every check also prints its counts, so a run that inspected little says so on stdout.

## Tests

`tests/inject.sh` is the failure-injection harness: **120 arms**, each asserting the
refusal **text** and not merely a non-zero status.

```bash
nix develop -c ./shellWrapper/dlint/tests/inject.sh                     # builds .#dlint from a clean tree
nix develop -c ./shellWrapper/dlint/tests/inject.sh --dlint /path/to/dlint
```

The fixture in `tests/fixtures/conforming` is laid out like neither of the repositories
`dlint` was extracted for: workflows in `ci/workflows`, CI entrypoints in `automation/`,
the trust map at `trust/actions.json`, the vendored tree at `third_party/skills`. A
diene-shaped or registry-shaped constant anywhere in `dlint` would fail these arms.
