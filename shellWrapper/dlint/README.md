# dlint

Five repo-agnostic repository linters behind one entrypoint. `dlint` runs in **any**
consuming repository: every repository-specific fact comes from that repository's own
configuration file, never from a constant baked into the tool.

```text
dlint <check> [args]
dlint --check <check> [--check <check>]...
dlint --all-configured
dlint --help
dlint --version
```

There are **exactly five** checks. There are no aliases and no hidden checks. There is no
`all` **check** either — running everything is the `--all-configured` flag. An unknown
check exits `2` and lists the valid ones.

| Check                                | Refuses when                                                                                                                                                                                                         |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `action-pins <trusted\|non-trusted>` | An action reference has no authored trust classification; a `trusted` action is not on a major tag (`v3`); a `non-trusted` action is not on an exact 40-hex SHA whose trailing comment names its tag.                |
| `exec-bits`                          | A tracked shell script is not executable, or is missing from the worktree.                                                                                                                                           |
| `ci-wiring`                          | A CI entrypoint a workflow names is missing or not executable; an orchestrator job does not call a repository-local reusable workflow; a reusable workflow calls no CI entrypoint; an orchestrator declares no jobs. |
| `skills-fresh`                       | The vendored tree moved in the worktree after its own regeneration command ran, or there is no tracked subject to judge at all.                                                                                      |
| `toolchain-smoke`                    | A required binary does not resolve in the named shell where the check is run.                                                                                                                                        |

## Several checks in one invocation

`--check` may be repeated, and each value is one check with its arguments. `--all-configured`
runs the lot. Both exist so a consuming repository can call `dlint` **once**, as a plain
binary, instead of wrapping it in a shell that chains invocations with `&&`.

```text
dlint --check 'action-pins trusted' --check 'action-pins non-trusted'
dlint --all-configured
```

Three properties hold, and each is asserted by its own arm in `tests/inject.sh`:

- **Every requested check runs**, even after one refuses, so a single invocation reports
  every fault instead of only the first. A `&&` chain stops at the first failure.
- **The exit code is the highest of theirs.** The codes are ordered by severity, so a check
  that could not be trusted (`3`, `4`, `5`) outranks a known violation (`1`). Reporting the
  violation and hiding the unrunnable check would be the quieter, worse answer.
- **`--all-configured` iterates the checks `dlint` ships, not the keys the file carries.**
  This is the important one. Deriving the work list from the configuration means deleting a
  section silently stops enforcing that check while the run still reports green — the
  vacuous pass every other refusal here exists to prevent. Because the population is
  `dlint`'s own list, an absent section reaches its normal `3`, and `"<check>": false`
  remains the one way to opt out. A configured key `dlint` does not know exits `4`, so a
  misspelled check cannot sit in the file looking enforced.

A configured `action-pins` expands to **both** trust classes: the check enforces one class
per invocation, so running only `trusted` would leave every non-trusted pin unasserted while
the invocation reported green.

## Configuration

All five checks read **one** file: `$DLINT_CONFIG`, or `./.dlint.json`. `dlint` is run
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
| `toolchain-smoke.shell`       | yes      | —                   |
| `toolchain-smoke.binaries`    | yes      | —                   |

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

`tests/inject.sh` is the failure-injection harness: **93 arms**, each asserting the
refusal **text** and not merely a non-zero status.

```bash
nix develop -c ./shellWrapper/dlint/tests/inject.sh                     # builds .#dlint from a clean tree
nix develop -c ./shellWrapper/dlint/tests/inject.sh --dlint /path/to/dlint
```

The fixture in `tests/fixtures/conforming` is laid out like neither of the repositories
`dlint` was extracted for: workflows in `ci/workflows`, CI entrypoints in `automation/`,
the trust map at `trust/actions.json`, the vendored tree at `third_party/skills`. A
diene-shaped or registry-shaped constant anywhere in `dlint` would fail these arms.
