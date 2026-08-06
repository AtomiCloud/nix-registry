# dlint

Six repo-agnostic repository linters behind one entrypoint. `dlint` runs in **any**
consuming repository: every repository-specific fact comes from that repository's own
configuration file, never from a constant baked into the tool.

```text
dlint <check> [args]
dlint --help
dlint --version
```

There are **exactly six** checks. There are no aliases, no hidden checks and no `all`.
An unknown check exits `2` and lists the valid ones.

| Check                                | Refuses when                                                                                                                                                                                                         |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `action-pins <trusted\|non-trusted>` | An action reference has no authored trust classification; a `trusted` action is not on a major tag (`v3`); a `non-trusted` action is not on an exact 40-hex SHA whose trailing comment names its tag.                |
| `exec-bits`                          | A tracked shell script is not executable, or is missing from the worktree.                                                                                                                                           |
| `ci-wiring`                          | A CI entrypoint a workflow names is missing or not executable; an orchestrator job does not call a repository-local reusable workflow; a reusable workflow calls no CI entrypoint; an orchestrator declares no jobs. |
| `skills-fresh`                       | The vendored tree moved in the worktree after its own regeneration command ran, or there is no tracked subject to judge at all.                                                                                      |
| `toolchain-smoke`                    | A binary declared for a shell does not resolve INSIDE that shell, or the shell could not be entered at all.                                                                                                          |
| `workflow-policy`                    | A declared path in a declared workflow file does not hold its declared value, or is absent. One assertion is one caught fault.                                                                                       |

## Configuration

All six checks read **one** file: `$DLINT_CONFIG`, or `./.dlint.json`. `dlint` is run
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
      "enter": "nix develop .#{shell} --command bash -c",
      "shells": {
        "default": ["bash", "git"]
      }
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

| Key                           | Required | Default             |
| ----------------------------- | -------- | ------------------- |
| `action-pins.trustMap`        | yes      | —                   |
| `action-pins.workflowsDir`    | no       | `.github/workflows` |
| `action-pins.requireSubjects` | no       | `true`              |
| `exec-bits.globs`             | no       | `["*.sh"]`          |
| `exec-bits.requireSubjects`   | no       | `true`              |
| `ci-wiring.entrypointPattern` | yes      | —                   |
| `ci-wiring.orchestrators`     | yes      | —                   |
| `ci-wiring.workflowsDir`      | no       | `.github/workflows` |
| `skills-fresh.regenerate`     | yes      | —                   |
| `skills-fresh.paths`          | yes      | —                   |
| `skills-fresh.ignore`         | no       | `[]`                |
| `workflow-policy.assertions`  | yes      | —                   |
| ` ↳ .file`                    | yes      | —                   |
| ` ↳ .path`                    | yes      | —                   |
| ` ↳ .equals`                  | yes      | — (may not be null) |
| ` ↳ .reason`                  | yes      | —                   |

| `toolchain-smoke.shells` | yes\* | — |
| `toolchain-smoke.enter` | yes\* | — (needs `{shell}`) |
| `toolchain-smoke.shell` | legacy | — |
| `toolchain-smoke.binaries` | legacy | — |

`yes*` means required **in the scoped form**. `shells` + `enter` is that form; `shell` +
`binaries` is the legacy single-shell one. Declare one form or the other, never both.

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

`toolchain-smoke` **enters each declared shell** and resolves that shell's binaries inside it,
so a green result is attributable to the shell it names. It does not inspect package
declarations: a developer-installed binary must not make a declaration-only check pass.

```json
{
  "checks": {
    "toolchain-smoke": {
      "enter": "nix develop .#{shell} --command bash -c",
      "shells": {
        "default": ["bash", "git"],
        "cd": ["skopeo", "helm"]
      }
    }
  }
}
```

`enter` must contain `{shell}`. It is split on whitespace into an argv, `{shell}` is
substituted, and the probe is appended as **one** final argument — so it has to end in
something that takes a script string, like `bash -c`.

### Why per-shell, and not one shell

This check used to be configured `"shell": "default"` and then resolve binaries in whatever
environment `dlint` happened to be invoked from. It reported that `skopeo` resolves, and it
was **right** — about a shell the failing probe never used (`.#cd`). _A control that answers
a neighbouring question is worse than none, because it retires the doubt without answering
it._ Per-shell scoping is what makes the answer attributable: **resolution alone was never
the missing part; attribution was.**

Two consequences are load-bearing, and each has its own arm:

- **A binary present in one shell and absent in another produces two different verdicts**,
  and the refusal names the shell. Under an implementation that inspects only the invocation
  environment, the two shells are indistinguishable — which is why that arm is the one worth
  reading first.
- **"Could not enter the shell" is exit `5`, not a missing binary.** Both would otherwise
  exit non-zero, and collapsing them lets a broken entry mechanism read as a missing binary:
  a wrong reason, loudly stated. The probe therefore reports through a marker rather than
  through its exit status, and a missing marker is UNKNOWN.

### The legacy single-shell form

`shell` + `binaries` keeps working, so a repository configured for it does not break the
moment this lands. Declaring **both** `shell` and `shells` is a configuration error: it would
leave ambiguous which shell a green result is about. The legacy form now states what it
actually inspected:

```text
ℹ️ toolchain binaries inspected: 24 in the INVOCATION ENVIRONMENT (declared shell: default, not entered)
✅ The invocation environment resolves every binary declared for 'default'
```

It no longer claims the named shell resolved anything, because it never entered it.

## Workflow policy

`workflow-policy` replaces hand-written `yq | jq` validators. The policy is
repository-specific, so every assertion is declared: the file, the path, the expected value,
and the reason to print when it does not hold.

```json
{
  "checks": {
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

`tests/inject.sh` is the failure-injection harness: **107 arms**, each asserting the
refusal **text** and not merely a non-zero status.

```bash
nix develop -c ./shellWrapper/dlint/tests/inject.sh                     # builds .#dlint from a clean tree
nix develop -c ./shellWrapper/dlint/tests/inject.sh --dlint /path/to/dlint
```

The fixture in `tests/fixtures/conforming` is laid out like neither of the repositories
`dlint` was extracted for: workflows in `ci/workflows`, CI entrypoints in `automation/`,
the trust map at `trust/actions.json`, the vendored tree at `third_party/skills`. A
diene-shaped or registry-shaped constant anywhere in `dlint` would fail these arms.
