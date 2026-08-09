# resolver-smoke

`resolver-smoke` is a **template-authoring / meta-space gate**. It belongs to the people who
write and change cyanprint templates, and it runs in meta-space: by cascade execution before
a push, and in meta-space repository CI. It must **NEVER** be wired into a shipped
`.pre-commit-config.yaml`, into a template's `nix/pre-commit.nix`, or into any other
generated-repo consumer configuration. A generated repository is not the thing under test —
the template that produced it is — so putting this check in a consumer's hooks would make
every downstream repository pay for a gate that cannot tell them anything actionable.

It proves that a repository's resolver-managed files are resolver-friendly by **running the
real published resolver over them**, never by approximating its grammar with a static shape
check. That distinction is the whole point. The defect this tool exists for was found on
2026-08-09: given a multi-set `//` `nix/packages.nix`, the published `atomi/nix@2` packages
merger silently collapsed it to an empty 42-byte skeleton **and exited 0**. Nothing about
that file looks wrong; it is simply outside the merger's grammar. Only running the merger
finds it.

## What it does

Run from the repository root with no arguments. `resolver-smoke` reads which files exist
among the nix dispatch table:

```text
flake.nix
nix/env.nix
nix/fmt.nix
nix/packages.nix
nix/shells.nix
nix/pre-commit.nix
```

For each file that is present it builds a **structure-aware synthetic child** — a
well-formed second layer carrying a sentinel binding, shaped for that particular file — hands
the real bytes and the child to the vendored published merger, and asserts four things:

1. the merge does not throw on a **well-formed** child;
2. no binding present in the real file is lost;
3. the sentinel survives into the output;
4. the output is **non-degenerate** — it carries at least the material content of the larger
   input, and is never an empty skeleton.

Any failure is a loud refusal naming **the file and the probe** that failed. A run that would
inspect nothing refuses rather than reporting green; see `requireSubjects` below.

## Usage

```text
resolver-smoke                    # probe every dispatch-table file present in this repo
resolver-smoke --config <path>    # read configuration from an explicit path
resolver-smoke --full             # print every lost unit and every disclosure block
resolver-smoke --json             # emit one machine-readable report, no human lines
resolver-smoke --help
resolver-smoke --version
```

There are no subcommands. `resolver-smoke` is run from the repository root and reads every
path relative to it. `--full` and `--json` are accepted in any order, alone or together, and
combine freely with `--config`. `--json` implies `--full` detail — a machine-readable report
that truncated its own lists would be the defect the mode exists to remove — and suppresses
every `✅`, `❌` and `ℹ️` line so stdout is one JSON document. Exit codes are identical in
every mode; the flags change what is printed and nothing else.

**The default is TRUNCATED**, and that is deliberate: a pre-push gate whose refusal is forty
lines long gets skimmed. But a truncated list cannot serve as a **specification**, and on
2026-08-09 one tried to. A hoist specification written against this tool
(`diene/.ratchet-ops/handoffs/HOIST-SPEC-dotnet-base-cyanprint.md` §4) had to reconstruct part
of its own subject matter by inferring the published resolver's sort order from the names it
could see, and got the reconstruction wrong: it guessed "5 distinct names", reasoned only over
bindings, and so missed the three lost `inherit` units entirely. If you are writing anything
that has to be **complete**, use `--full` or `--json`.

### The two caps, and which disclosure is a bound

Lost material is reported by two different lists, with two different caps, and only one of
them is resolver-smoke's:

| Cap    | Whose it is                                                        | Escapable from here?                                           |
| ------ | ------------------------------------------------------------------ | -------------------------------------------------------------- |
| **16** | resolver-smoke's own `[<arm>/material-survival]` finding           | **Yes** — `--full` / `--json` print the whole list.            |
| **24** | `assertNoLoss` in the published bundle, relayed by `[<arm>/merge]` | **No.** See below; `assertNoLoss` is the only possible source. |

The 24 cannot be widened from here because of _how_ the guard refuses: `assertNoLoss` computes
the merged output, sorts the complete lost list, slices the first 24 names into a message, and
then **throws instead of returning the output**. The merged output is the only thing that could
say which names sit past the cap, and the throw discards it. The bundle exports exactly one
symbol (`resolver`), which `src/vendor.ts` asserts, so there is nowhere else to look. The
withheld names are therefore not recoverable by any honest means, and resolver-smoke does not
re-implement the mergers, mutate the subject to slide the disclosure window, or patch the
vendored module to pretend otherwise. A wider disclosure has exactly one home: `assertNoLoss`
in the published bundle.

What `--full` and `--json` do deliver, completely, is four blocks per refusing arm:

| Block                     | What it is                                                                                                                                              |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| the `ℹ️` counts line      | disclosed / withheld / total for this arm, and the counting semantics. Printed in **every** mode, including the default.                                |
| `disclosed`               | every unit the published message named, parsed back into `(kind, name)` pairs. Exact, complete, all 24.                                                 |
| `child-contributed`       | the guarded units resolver-smoke's own synthetic child brings that the real file does not have. Exact — resolver-smoke generated those bytes.           |
| `candidate (upper bound)` | **a bound, not a result.** Every guarded unit of this arm's inputs that sorts strictly after the last disclosed unit under the bundle's own comparator. |

The bound is sound because the guard sorts before it slices: every withheld name provably sits
inside that set. It is not tight, because it also contains units that survived the merge — that
is exactly what resolver-smoke cannot see. When the bound's size equals the withheld count the
remainder **is** determined and the output says so in as many words
(`remainder DETERMINED: the withheld names are exactly the N candidate(s) above`); until then
it is labelled `upper bound` on every line it appears.

### Counting semantics — settled, not open

A count of lost material is **DISTINCT `(kind, name)` units, deduplicated across every probe
input of the arm. It is never a count of occurrences.** This is not an interpretation; it is
what the bundle does, and there are two pieces of evidence in `vendor/nix.mjs`:

- `check()` keys its `seen` set on `` `${kind}:${value}` `` **before** pushing, so a name lost
  from two attrsets counts **once** — and a name lost as both a binding and an inherited
  identifier counts **twice**, because the kinds differ;
- `lostUnits` iterates `for (const input of inputs)`, so on a two-input arm the **synthetic
  child's own material is inventoried too**. The child is not a passive second layer.

The observation that settled it was measured on 2026-08-09 against
`diene.all@88418396:nix/packages.nix`, by running the vendored mergers with the loss guard
removed in a throwaway scratch copy and inventorying the real merged output:

| arm                   | inputs | distinct lost units | disclosed | withheld |
| --------------------- | ------ | ------------------- | --------- | -------- |
| `self-probe`          | 1      | **32**              | 24        | **8**    |
| `synthetic-child`     | 2      | **34**              | 24        | **10**   |
| `input-order-control` | 2      | **34**              | 24        | **10**   |

The 8 → 10 difference is the synthetic child contributing exactly **2** units of its own —
`inherited identifier 'pkgs-2605'` and the sentinel. `injectPackages` splices the sentinel
directly after the first `inherit` keyword, which detaches `(pkgs-2605)` from its
inherit-source position, so the child inventories `pkgs-2605` as an inherited identifier where
the real file does not. `--full` prints that contribution per arm, which is why the per-arm
residual delta is now readable rather than something to be guessed at.

### Every count in the summary states its unit

The same class of defect had one more instance, in the closing line rather than in the lists.
It used to read:

```text
❌ resolver-smoke: 6 refusal(s) across 6 resolver-managed file(s) in 70ms
```

The trailing number was the count of files **scanned**, not the count of files that refused —
which on that run was **2**. Read naturally the line claims the whole tree refused, and a reader
came close to quoting that fraction. A refusal count, a refusing-file count and a scanned-file
count are three different numbers, and on a single-file subject all three agree, which is how the
ambiguity survived. It now reads:

```text
❌ resolver-smoke: 6 refusal(s) across 2 refusing file(s) of 6 resolver-managed file(s) scanned, in 70ms
```

`--json`'s `summary` carries the same three as separately named keys — `refusals`,
`refusingFiles`, `scannedFiles` — plus `passedFiles`, so the figures reconcile rather than having
to be trusted. Group `L` holds the line to it against a tree where all three counts differ.

## Configuration

Configuration is **optional**. With no file, `requireSubjects` is `true`, which is the
correct default: a repository with none of the dispatch-table files has had nothing proved
about it, and that is not a pass.

`resolver-smoke.yaml` is the canonical name. `resolver-smoke.yml`,
`resolver-smoke.json` and `.resolver-smoke.json` are also read. Finding **more than one** is
an error (`4`), not a precedence rule — whichever one was not read would sit in the tree
looking as though it configured something.

```yaml
schemaVersion: 1
requireSubjects: false
```

Those are the only two keys; any other key is a configuration error. `$RESOLVER_SMOKE_CONFIG`
overrides the search with an explicit path, and `--config <path>` overrides that in turn. A
path that is named but does not exist is an error, never a silent fall-back to the default
search.

`requireSubjects: false` is the one way to declare that a repository legitimately has **no**
resolver-managed files. It is a declaration, never a default — the same rule `dlint` follows,
for the same reason: a green result after zero subjects is the failure mode this whole class
of tool exists to prevent.

## Exit codes

| Code | Meaning                                                                                               |
| ---- | ----------------------------------------------------------------------------------------------------- |
| `0`  | every present dispatch-table file survived every probe                                                |
| `1`  | a probe refused — the merger rejected well-formed input, lost material, or produced degenerate output |
| `2`  | usage error                                                                                           |
| `4`  | invalid configuration, or the vendored resolver failed its integrity check                            |
| `5`  | the tool could not complete the inspection because of an internal read, import, or result failure     |

`4` deliberately covers **both** a bad configuration and a bad vendored bundle: in each case
the tool could not be trusted to have inspected what it claims, which is a different and
more serious answer than "the repository is wrong".

## The vendored resolver

The published `atomi/nix@3` bundle is **vendored** at `vendor/nix.mjs`. It is the bundle
as re-downloaded from the CyanPrint registry on 2026-08-09, and it is committed rather than
fetched so that the check is **hermetic**: `resolver-smoke` reaches the network at no point,
which is what lets it run as a pre-push gate on a machine that may be offline.

Its exact provenance is:

- registry metadata: `https://registry.cyanprint.dev/artifacts/resolver/atomi/nix/versions`;
- artifact: `resolver__atomi__nix__3`, published `2026-08-09T10:55:18.053Z`;
- bundle object: `resolver/atomi/nix/96da38b1-e0e9-4c9a-ac2e-016ecabc9289/bundle/bundle.js`.

The metadata endpoint returns the bundle's object reference. To reproduce the download,
POST that `object` as `{ "ref": <object> }` to
`https://registry.cyanprint.dev/objects/download` with
`Accept: application/octet-stream`, then pass the downloaded file to the refresh script.
This records where the bytes came from without duplicating the authoritative digest.

`vendor/SHA256` holds the recorded digest, and it is the **single** place that constant is
written. It is asserted twice, at two different moments:

- **at build time**, by `default.nix`'s `installCheckPhase`, so a bundle that does not match
  cannot even be built into a package;
- **at run time**, by `src/vendor.ts`, before the module is imported, so a bundle substituted
  after the build cannot be used.

Neither assertion is redundant. Verify it yourself at any time:

```console
$ sha256sum bunWrapper/resolver-smoke/vendor/nix.mjs
14ebea789a5e6199992ef197f2ae9e3b4db8a33ceb3be6ee5659b4725ed6680d  bunWrapper/resolver-smoke/vendor/nix.mjs
$ cat bunWrapper/resolver-smoke/vendor/SHA256
14ebea789a5e6199992ef197f2ae9e3b4db8a33ceb3be6ee5659b4725ed6680d
```

The digest above is a transcript, not a second copy of the constant: `vendor/SHA256` is
authoritative, and a digest written into prose goes stale on the first refresh.

Because the bundle's identity **is** its bytes, prettier must not touch it. `*.mjs` is in
treefmt's prettier include list, so the bundle is listed in the repository-root
`.prettierignore`. Reformatting it would change the bytes without changing what the program
does, and the integrity check would then refuse on content that is, in fact, exactly what was
published.

### Refreshing the bundle

Refreshing is **manual and deliberate**, never automatic and never at hook time. From the
repository root, pass the published bundle you downloaded from the registry (or its URL) to
the refresh script:

```bash
./bunWrapper/resolver-smoke/scripts/refresh-vendor.sh <published-nix-bundle-path-or-url>
nix build path:.#resolver-smoke
./bunWrapper/resolver-smoke/tests/run.sh
```

Do not hand-edit `vendor/SHA256` to match whatever is on disk. That turns the guard into a
rubber stamp, and the tool's only claim — that it ran _the published_ merger — stops being
true.

A refresh must also **re-check two things that are now load-bearing**, because the
complete-list mode makes claims about the bundle's internals rather than only relaying its
output:

1. **The 24-name cap in `assertNoLoss`.** `PUBLISHED_DISCLOSURE_CAP` in `src/loss.ts` is the
   number resolver-smoke reports and documents. If the published guard changes it — or changes
   the message wording the parser bounds the unit list with — the loss detail must be
   re-derived, not adjusted to fit.
2. **The guarded-kind equivalence with `inventoryMaterial`.** `args`, `bindings`, `inherited`
   and `withPreludes` in `src/probes/material.ts` are kept byte-equivalent to the bundle's
   `inventoryMaterial`, regexes included. The candidate remainder is only a sound bound while
   that holds: a kind resolver-smoke cannot see is a withheld name the bound cannot cover.

Neither is checked automatically, because a check that read the bundle's source to confirm what
the bundle does would pass on a bundle that had stopped doing it.

## Tests

Every probe is proved by **injection**, not by being green. The fixtures live in
`tests/fixtures/` and deliberately carry a `.nixsrc` extension rather than `.nix`: they are
inputs whose exact bytes the assertions depend on, and `nixpkgs-fmt` normalises every `.nix`
file in this tree. The extension keeps them out of the formatter's reach without needing an
exclusion, which is why nothing in `nix/fmt.nix` had to change.

| Fixture                                    | Provenance                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tests/fixtures/canonical/**`              | The canonical shape a cyanprint template is expected to have — `flake.nixsrc` plus the five `nix/*.nixsrc`. Every probe must pass on these.                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `tests/fixtures/collapse/packages.nixsrc`  | The **exact** defect shape, copied verbatim from the diene workspace's pre-fix `nix/packages.nix` on 2026-08-09: a function whose body is a `//` chain of several `with`-scoped sets. This must **fail**, and the refusal must carry the merger's own reason.                                                                                                                                                                                                                                                                                                                                     |
| `tests/fixtures/wide-loss/packages.nixsrc` | Purpose-built for group `K`, and **acme-shaped on purpose** — arm `1`'s whole point is that no workspace constant is baked in, and a fixture that reintroduced one would undo it. It reproduces the real subject's _shape_ (a `stdenvNoCC.mkDerivation` in the top-level `let`, two per-system attrsets selected by `.${system}`, a `root = { inherit …; }` exposure attrset), which is what carries a `packages.nix` past the published guard's 24-name cap. Its byte length and sha256 are re-asserted on every use: those bytes decide which names land inside the cap and which fall past it. |

The battery is **116 arms across twelve groups**, and every arm declares which of two directions
it proves. The distinction matters because the vendored bundle fixed several of the defects
the battery was originally written against, and an arm that quietly changed direction without
saying so would look like coverage while proving the opposite of what its name claims.

| Group | Direction  | What it holds                                                                                                                                                                                                                                                                         |
| ----- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `A`   | interface  | `--help`, `--version`, and refusal to guess at an unknown argument or positional.                                                                                                                                                                                                     |
| `B`   | positive   | The canonical six each report their own passing probe, and the run counts all six.                                                                                                                                                                                                    |
| `C`   | refusal    | The exact pre-fix collapse bytes are a violation naming `nix/packages.nix`, and the refusal carries the published merger's reason rather than a bare non-zero exit.                                                                                                                   |
| `D`   | refusal    | A one-byte-corrupted bundle is exit `4`, naming both the expected and the actual sha256 — the arm that keeps the integrity check from becoming decorative.                                                                                                                            |
| `E`   | refusal    | Vacuity refuses, and only a declared `requireSubjects: false` turns that into a pass. Bad configuration is exit `4`, never a silent off switch.                                                                                                                                       |
| `F`   | positive   | Each documented configuration name is really read, and an undocumented one is really not.                                                                                                                                                                                             |
| `G`   | regression | The three shapes the `@2` mergers lost silently — a multi-line header on `nix/packages.nix` and `nix/shells.nix`, a leading comment above the header on `nix/env.nix` — now merge losslessly and the gate reports the file **passing**. These are the guard that the fix stays fixed. |
| `H`   | mixed      | Alien constructs the published merger refuses (`H1`–`H3`, `H5`, `H6`), each asserting the relayed reason; plus `H4`, a regression arm for the formatter option `@2` dropped and `@3` preserves.                                                                                       |
| `I`   | positive   | Comments, blank lines, trailing whitespace and a partial subject set are all benign. A gate that fires on a comment blocks every commit.                                                                                                                                              |
| `J`   | positive   | The canonical baseline is re-asserted after the whole mutation battery, and the run is inside its 3s budget.                                                                                                                                                                          |
| `K`   | disclosure | Both truncated lost-material lists can be escaped with `--full` / `--json`, and escaping them changes nothing else. `K3` is a MUST-DIFFER pair, so the flag cannot decay into a no-op; `K12` holds the new `ℹ️` line to being informational rather than a refusal.                    |
| `L`   | disclosure | Every count in the summary line states its unit. The wide-loss tree refuses 3 times in 1 file out of 6 scanned, so all three figures differ and an assertion cannot pass by picking up the wrong one. `L4` is the MUST-DIFFER arm holding the old, ambiguous shape out of the output. |

A **regression** arm still injects its shape and still runs the injection guards, so it cannot
pass by decaying into an unmutated canonical run. A **refusal** arm asserts the refusal TEXT and
not merely a non-zero exit; a gate that is only non-zero has not been shown to say why.

Three of the refusal arms (`H2`, `H5`, `H6`, on `nix/packages.nix`, `nix/env.nix` and
`nix/pre-commit.nix`) provoke the bundle's own loss guard — the wrapper that inventories every
function argument, `with` prelude, inherited identifier and binding of each input and throws
when one would not survive the merge. resolver-smoke relays that throw as a violation naming the
file, which is the gate working: a shape the resolver will not merge is exactly what the
operator needs to be told about. Covering three different files keeps a single merger going
quiet from taking the whole proof with it.

```bash
./bunWrapper/resolver-smoke/tests/run.sh
RESOLVER_SMOKE="$(nix build --no-link --print-out-paths path:.#resolver-smoke)/bin/resolver-smoke" ./bunWrapper/resolver-smoke/tests/run.sh
```

## Speed

The whole run must stay **under 3 seconds**; it measures around 1 second today. This is a
budget, not an observation. A pre-push gate that takes longer than the push does gets
disabled, and a disabled gate catches nothing — so if a new probe pushes the run over the
budget, the probe is what has to change.

## Extensibility

v1 covers the `atomi/nix` resolver only. Further resolvers — `sh`, `ignore`, `md` — get their
own probe modules under `src/probes/`, each declaring the files it owns and how to build a
well-formed synthetic child for them. **The CLI surface does not change to add one**: adding
and registering a probe module requires no new flag, subcommand, or configuration key.
