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
resolver-smoke --help
resolver-smoke --version
```

There are no subcommands. `resolver-smoke` is run from the repository root and reads every
path relative to it.

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

The published `atomi/nix@2` bundle is **vendored** at `vendor/nix-v2.mjs`. It is the bundle
as re-downloaded from the CyanPrint registry on 2026-08-09, and it is committed rather than
fetched so that the check is **hermetic**: `resolver-smoke` reaches the network at no point,
which is what lets it run as a pre-push gate on a machine that may be offline.

Its exact provenance is:

- registry metadata: `https://registry.cyanprint.dev/artifacts/resolver/atomi/nix/versions`;
- artifact: `resolver__atomi__nix__2`, published `2026-08-09T07:44:03.519Z`;
- bundle object: `resolver/atomi/nix/184b3aed-fce7-4587-b96c-55fd69ea51b8/bundle/bundle.js`.

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
$ sha256sum bunWrapper/resolver-smoke/vendor/nix-v2.mjs
73dd848c49cf1ab1894fbb1f455ce066896cc1dfa39a42865926ea6f8c8182dd  bunWrapper/resolver-smoke/vendor/nix-v2.mjs
$ cat bunWrapper/resolver-smoke/vendor/SHA256
73dd848c49cf1ab1894fbb1f455ce066896cc1dfa39a42865926ea6f8c8182dd
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
./bunWrapper/resolver-smoke/scripts/refresh-vendor.sh <published-nix-v2-bundle-path-or-url>
nix build path:.#resolver-smoke
./bunWrapper/resolver-smoke/tests/run.sh
```

Do not hand-edit `vendor/SHA256` to match whatever is on disk. That turns the guard into a
rubber stamp, and the tool's only claim — that it ran _the published_ merger — stops being
true.

## Tests

Every probe is proved by **injection**, not by being green. The fixtures live in
`tests/fixtures/` and deliberately carry a `.nixsrc` extension rather than `.nix`: they are
inputs whose exact bytes the assertions depend on, and `nixpkgs-fmt` normalises every `.nix`
file in this tree. The extension keeps them out of the formatter's reach without needing an
exclusion, which is why nothing in `nix/fmt.nix` had to change.

| Fixture                                   | Provenance                                                                                                                                                                                                                                                    |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tests/fixtures/canonical/**`             | The canonical shape a cyanprint template is expected to have — `flake.nixsrc` plus the five `nix/*.nixsrc`. Every probe must pass on these.                                                                                                                   |
| `tests/fixtures/collapse/packages.nixsrc` | The **exact** defect shape, copied verbatim from the diene workspace's pre-fix `nix/packages.nix` on 2026-08-09: a function whose body is a `//` chain of several `with`-scoped sets. This must **fail**, and the refusal must say the output was degenerate. |

A corrupted `vendor/nix-v2.mjs` must exit `4`, not pass — that arm is the one that keeps the
integrity check from quietly becoming decorative.

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
