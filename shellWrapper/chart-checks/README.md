# chart-checks

Fetch pinned policy and schema inputs, then validate rendered Kubernetes manifests **offline**.
One binary, two commands, one config file at the consumer repository root.

```text
chart-checks install [-c chart-checks.yaml]            fetch + verify into .chart-checks/
chart-checks check   [-c chart-checks.yaml] <file>...  validate rendered manifests offline
chart-checks --version | --help
```

First consumer: the `helm-wrapper` template's `a-wrapper-manifests` pre-commit hook (and through
it every `charts/*` instance); `charts/vap-policies` runs it on itself with `path` entries.

## Exit codes

| Code | Meaning                                                                                    |
| ---- | ------------------------------------------------------------------------------------------ |
| 0    | install done or up to date; every manifest passed                                          |
| 1    | a manifest was refused (schema stage, policy stage, or both; both always run)              |
| 2    | usage error (unknown command or option, `check` with no file, a file that does not exist)  |
| 3    | `chart-checks.yaml` refused: unknown key, missing pin, `temporary` without a `reason`, ... |
| 4    | install failed: fetch error, sha256 or commit mismatch, an entry that yields no CRD        |
| 5    | tool error: a policy chart did not render or holds no `ValidatingAdmissionPolicy`          |

## `install`

Reads `chart-checks.yaml`, fetches every entry into `.chart-checks/` (next to the config) and
verifies each pin. A mismatch or a missing pin is refused loudly and leaves **no** `.chart-checks/`
behind (the install is staged and moved into place only when everything verified).

**Offline when present.** `install` writes `.chart-checks/stamp`, a hash of the list (the
canonical JSON of the config, so comments and formatting do not count). When the stamp matches,
`install` does nothing and opens no network connection. When the list changes it re-fetches
everything. CI caches `.chart-checks/` keyed on `hashFiles('chart-checks.yaml')`.

| Source | Fetched with                            | Pin (required)                              |
| ------ | --------------------------------------- | ------------------------------------------- |
| `oci`  | `helm pull <ref> --version <v>`         | `version` and `sha256` of the pulled `.tgz` |
| `url`  | `curl`                                  | `sha256` of the downloaded file             |
| `git`  | `git fetch` of one commit (`--depth 1`) | `commit`, all 40 hex characters             |
| `path` | nothing; read in place by `check`       | none (a local directory or file)            |

## `check`

Reads only `.chart-checks/` and the `path` entries. If the folder is missing or its stamp does not
match the list, it runs `install` first. It fetches nothing else. Both stages always run, so one
invocation reports everything.

1. **Schemas.** `kubectl-validate --local-crds <installed CRDs> --version <kubernetes>`: the core
   kinds, every installed CRD, and **unknown fields are refused**.
2. **Policies.** Every `policies` entry is rendered with `helm template`, only the
   `ValidatingAdmissionPolicy` objects are kept (a `ValidatingAdmissionPolicyBinding` is never
   handed over), and `kyverno apply <VAPs> --resource <file> --detailed-results --remove-color`
   evaluates them. A chart that renders no VAP is refused (exit 5), never passed vacuously.
   There is no kind pre-filter: each VAP's `matchConstraints` selects.

> `kyverno apply` cannot map a CRD kind (HTTPRoute, ExternalSecret, ...) to its resource name
> offline unless it is given that CRD, and its `--crd-path` accepts exactly **one** CRD file. So
> `check` splits the input into single objects, groups them by kind, and runs kyverno once per
> kind that an installed CRD defines (with that CRD) plus once for all other kinds. Every policy
> still sees every object.

## `chart-checks.yaml`

```yaml
kubernetes: '1.30' # core schemas for offline validation; 1.23 to 1.30 only (see Step 0)
policies: # charts rendered with `helm template`; only their VAPs are used
  - name: vanadium
    oci: oci://ghcr.io/example/vanadium
    version: 1.2.3
    sha256: '<sha256 of the pulled .tgz>'
crds: # CustomResourceDefinitions the schema stage knows
  - name: gateway-api
    url: https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml
    version: v1.2.1 # a label for url entries; the sha256 is the pin
    sha256: '<sha256 of the downloaded file>'
  - name: dependency-operator
    git: https://github.com/ProtoDurian/diene.all
    commit: '<40 hex>'
    subpath: infra/root_chart/templates/crds
    temporary: true
    reason: the operator has not published its chart yet
  - name: logto-operator # a whole-object JSON Schema, wrapped into a CRD
    git: https://github.com/ProtoDurian/diene.all
    commit: '<40 hex>'
    subpath: schemas/logtoapp.json
    format: json-schema
    group: logto.example.io
    kind: LogtoApp
    crdVersion: v1
    temporary: true
    reason: no LogtoApp CRD exists yet
```

Keys per entry: `name` plus exactly one of `oci` / `url` / `git` / `path`; `version`, `sha256`,
`commit` (pins, see above); `subpath` (where inside the source the content is; defaults to the
chart's `crds/` for `oci`, the root otherwise); `plainHttp: true` (OCI over plain HTTP, for a
loopback registry); `temporary: true` with a non-empty `reason`; for `crds` only,
`format: json-schema` with `group`, `kind`, `crdVersion` (and optional `plural`). An unknown key is
refused (a typo such as `sha265` must not look enforced).

**Quote every hash.** An unquoted all-digit or `1e5`-shaped hex value is parsed by YAML as a
number; `chart-checks` refuses it with a message saying so.

A `json-schema` entry takes a whole-object JSON Schema, uses its `properties.spec` as the CRD's
spec schema, turns `const` into a one-value `enum` and drops `additionalProperties: false` (a
CRD structural schema forbids it next to `properties`; unknown fields are refused regardless).

## Step 0: `kubectl-validate` or `kubeconform` + a converter?

Decided on a fixture: **`kubectl-validate`** (nixpkgs 0.0.4); no CRD converter was written.

Measured, with every source gone and inside a nix build sandbox (no network):

- Core kinds validate against schemas **embedded for Kubernetes 1.23 to 1.30**. Any other
  `--version` makes it download the OpenAPI spec from `api.github.com`, which `check` must never
  do, so the tool refuses `kubernetes:` outside 1.23 to 1.30 (exit 3).
- `--local-crds <dir>` makes installed CRDs validate with no converter: the real Gateway API
  `v1.2.1` `standard-install.yaml` (5 CRDs) and External Secrets `v0.10.7` `bundle.yaml` (15 CRDs),
  both ways round (a good HTTPRoute / ExternalSecret passes, an unknown field is refused).
- Unknown fields are refused on core kinds and on CRD kinds, by default.

So the offline core-schema requirement is met by the validator carrying them, not by a pinned
bundle. The ceiling is Kubernetes 1.30; a newer core version would need a pinned schema bundle
passed with `--local-schemas`, which this version does not do.

## Tests

`tests/run.sh` is the harness; `nix build .#chart-checks-tests` runs it (also a flake check and a
CI package). A nix build has no network, so passing is the proof that `check` and a
stamp-matching `install` stay offline; the harness also deletes every source after `install` and
expects `check` to keep passing. Sources are `file://` URLs, a local git repository and, on
Linux, a loopback OCI registry (`oci` is not exercised on macOS: the arms use `git` instead).

Arms: good fixture passes (core kinds, ExternalSecret, HTTPRoute, a JSON-schema kind); `:latest`
image red (kyverno); unknown field on a core kind, a CRD kind and the JSON-schema kind red; a VAP
binding inside the policy chart ignored; sha256 mismatch refused; unknown commit refused; missing
pin refused; `temporary` without a `reason` refused; unknown key refused; unsupported Kubernetes
version refused; changed list re-installs; stale stamp makes `check` install first; `path`
entries need no install and see local edits at once; a policy chart with no VAP refused.

```bash
nix build .#chart-checks-tests -L
```

## What it carries

`writeShellApplication`-style, shellcheck-checked at build time, with every binary in
`runtimeInputs` (nothing is assumed on the caller's `PATH`): `bash`, `coreutils`, `curl`,
`findutils`, `git`, `gnugrep`, `gnutar`, `gzip`, `jq`, `kubectl-validate`, `kubernetes-helm`
(`helm`), `kyverno`, `yq-go`. A consumer's dev shell therefore needs only `chart-checks`.
