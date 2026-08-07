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
| `action-pins <trusted\|non-trusted>` | A `trusted` action (one matching `trustedPattern`) is not on a major tag (`v3`); any other action — non-trusted by default — is not on an exact 40-hex SHA whose trailing comment names its tag.                |
| `exec-bits`                          | A tracked shell script is not executable, or is missing from the worktree.                                                                                                                                           |
| `ci-wiring`                          | A CI entrypoint a workflow names is missing or not executable; an orchestrator job does not call a repository-local reusable workflow; a reusable workflow calls no CI entrypoint; an orchestrator declares no jobs. |
| `toolchain-smoke`                    | A binary declared for a shell does not resolve INSIDE that shell, resolves but its probe (default `--version`) exits non-zero, or the shell could not be entered at all.                                                                                                          |
| `no-custom-derivations`              | A declared nix file uses a custom derivation builder instead of staying a plain declarative list.                                                                                                                    |

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

All five checks read **one** file, and `dlint.yaml` is its canonical name (**D6
ONE-CONFIG-NAME**). `dlint` looks for, in order:

| Looked for      | Format                                                          |
| --------------- | --------------------------------------------------------------- |
| `$DLINT_CONFIG` | YAML if the path ends `.yaml`/`.yml`, else JSON                 |
| `./dlint.yaml`  | YAML — the canonical name                                       |
| `./.dlint.json` | JSON — still read, so a repository can migrate on its own clock |

Finding **both** `./dlint.yaml` and `./.dlint.json` is an error (`4`), not a precedence rule:
whichever one `dlint` did not read would sit in the tree looking enforced while configuring
nothing, which is the same silent staleness this tool refuses everywhere else.

The two forms are the **same configuration**, so this:

```yaml
schemaVersion: 1
checks:
  exec-bits:
    globs: ['*.sh']
  toolchain-smoke:
    shell: default
    binaries: [bash, git]
```

and the JSON below describe the same thing. YAML is converted to JSON once per invocation and
every reader is unchanged, so there is one code path rather than two that happen to agree
today — and there are arms running all five checks AND `--all-configured` from a YAML config to hold that.

Two properties are deliberate:

- **Every message names the file you wrote**, never the converted temporary. A refusal that
  pointed at a path in `/tmp` would be unactionable, so there is an arm asserting the refusal
  says `dlint.yaml`.
- **A YAML read failure is `4`, and an empty YAML document is `4`.** The conversion is not
  piped into `jq`: a pipeline takes its status from the last command, so a failed `yq` would
  otherwise leave `jq` reading an empty file and could read as success. An empty document also
  converts to the four bytes `null`, which a reader would walk into as if it were a
  configuration, so the top level is asserted to be a mapping.

`dlint` is run from the repository root and reads every path relative to it.

```json
{
  "schemaVersion": 1,
  "checks": {
    "action-pins": {
      "trustedPattern": "^AtomiCloud/",
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
    "toolchain-smoke": {
      "enter": "nix develop .#{shell} --command bash -c",
      "shells": {
        "default": ["bash", "git"]
      }
    },
    "no-custom-derivations": {
      "paths": ["nix/packages.nix", "nix/env.nix"]
    }
  }
}
```

One mechanism, applied uniformly: a section per check, keyed by the check's own name.

| Key                                     | Required | Default             |
| --------------------------------------- | -------- | ------------------- |
| `action-pins.trustedPattern`            | yes      | —                   |
| `action-pins.workflowsDir`              | no       | `.github/workflows` |
| `action-pins.requireSubjects`           | no       | `true`              |
| `exec-bits.globs`                       | no       | `["*.sh"]`          |
| `exec-bits.requireSubjects`             | no       | `true`              |
| `ci-wiring.entrypointPattern`           | yes      | —                   |
| `ci-wiring.orchestrators`               | yes      | —                   |
| `ci-wiring.workflowsDir`                | no       | `.github/workflows` |
| `toolchain-smoke.shells`                | yes\*    | —                   |
| `toolchain-smoke.enter`                 | yes\*    | — (needs `{shell}`) |
| `toolchain-smoke.shell`                 | legacy   | —                   |
| `toolchain-smoke.binaries`              | legacy   | —                   |
| `no-custom-derivations.paths`           | yes      | —                   |
| `no-custom-derivations.forbid`          | no       | dlint's vocabulary  |
| `no-custom-derivations.requireSubjects` | no       | `true`              |
| ` ↳ .file`                              | yes      | —                   |
| ` ↳ .path`                              | yes      | —                   |

`yes*` means required **in the scoped form**. `shells` + `enter` is that form; `shell` +
`binaries` is the legacy single-shell one. Declare one form or the other, never both.

Only facts that are conventions of a **tool** rather than of a repository have defaults:
`.github/workflows` is GitHub's, `*.sh` is the shell's. Everything that describes how a
particular repository is laid out must be declared, because a default there would be the
tool guessing.

`action-pins` classifies by ONE regex: an action matching `trustedPattern` is trusted
(major-tag pin), everything else is non-trusted (exact SHA plus tag comment). There is
no list of non-trusted actions to maintain — not matching IS the classification, so an
action nobody thought about gets the strictest pin by default.

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

`toolchain-smoke` **enters each declared shell**, resolves that shell's binaries inside it,
**and runs each one**: an entry is `binary` or `binary <probe args>` (default probe
`--version`), and the probe must exit `0`. Resolution proves the PATH delivers a file;
the run proves the build behind it works — the difference a broken nix pin upgrade
hides, because a bad pin can still deliver a file that resolves and cannot execute. It
does not inspect package declarations: a developer-installed binary must not make a
declaration-only check pass.

```json
{
  "checks": {
    "toolchain-smoke": {
      "enter": "nix develop .#{shell} --command bash -c",
      "shells": {
        "default": ["bash", "git", "kubectl version --client"],
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

## No custom derivations

`no-custom-derivations` enforces **D7 RESOLVER-SHAPE**: template nix stays plain declarative
lists, and custom builds live in the registry.

```json
{
  "checks": {
    "no-custom-derivations": {
      "paths": ["nix/packages.nix", "nix/pre-commit.nix", "nix/env.nix"]
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

## Non-vacuity

**No check passes without having inspected something.** A `✅` after zero subjects is the
failure mode this repository's history keeps producing, so every check refuses instead:

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

`tests/inject.sh` is the failure-injection harness: **166 arms**, each asserting the
refusal **text** and not merely a non-zero status.

```bash
nix develop -c ./shellWrapper/dlint/tests/inject.sh                     # builds .#dlint from a clean tree
nix develop -c ./shellWrapper/dlint/tests/inject.sh --dlint /path/to/dlint
```

The fixture in `tests/fixtures/conforming` is laid out like neither of the repositories
`dlint` was extracted for: workflows in `ci/workflows`, CI entrypoints in `automation/`,
the trusted-actions regex `^acme/`, its per-shell tools under `tools/`. A
diene-shaped or registry-shaped constant anywhere in `dlint` would fail these arms.
