# AtomiCloud Nix Registry

AtomiCloud's Nix Registry for custom built packages. 


# Packages

- semantic generator
- upstash
- action-docs
- typescript-json-schema
- swagger-typescript-api
- dotnetsay
- dotnet-ef
- mirrord
- pls
- toml-cli
- nix-share
- aws-export-credentials
- cyanprint
- releaser

# Tool bundles

Aggregates that ship a whole toolchain in one derivation. `infrautils` and
`infralint` are the full bundles and are unchanged — use them unless you need to
strip something.

The axis-pure bundles exist for nodes that declare a toolchain **without** the
tools they strip. Each one belongs to exactly one axis, so a node composes the
sets for the axes it keeps: a `wo-docker` node takes `infrautils-core` +
`infrautils-k8s`, a `wo-helm` node takes `infrautils-core` +
`infrautils-docker`, and a node stripping both takes `infrautils-core` alone.

| Bundle | Binaries | Stripped by |
| --- | --- | --- |
| `infrautils` | union of the three rows below | — |
| `infrautils-core` | `garden`, `mirrord`, `tilt`, `tofu` | — |
| `infrautils-docker` | `docker`, `dockerd`¹, `dockerd-rootless`¹ | `wo-docker` |
| `infrautils-k8s` | `helm`, `k3d`, `kubectl`, `kubectx`, `kubens` | `wo-helm` |
| `infralint` | union of the three rows below | — |
| `infralint-core` | `kubeconform`, `kyverno`, `terraform-docs`, `tflint`, `tfsec`, `tfsec-checkgen`, `tfsec-docs` | — |
| `infralint-docker` | `hadolint`, `skopeo` | `wo-docker` |
| `infralint-helm` | `helm-docs`, `helmlint` | `wo-helm` |

¹ Linux only — nixpkgs builds `pkgs.docker` client-only off Linux.

`skopeo` sits on the docker axis because it is a container-image tool; it needs
no daemon, so a `wo-docker` node that genuinely wants registry inspection can
still compose `infralint-docker` on purpose.

Every bundle's `bin/` list above is **enforced**, not documentation.
`binWrapper/bundleContract.nix` declares it and fails the build on any
mismatch, including the assertion that each full bundle is exactly the union of
its three axis slices. Run it with:

```bash
nix build .#tool-bundle-contract
```

Changing what a bundle provides therefore means changing that file too — a
tool entering or leaving a bundle can never be a silent diff again.

# Installation

To use latest registry

```bash
nix shell github:AtomiCloud/nix-registry
```

To add to flakes
```nix
atomipkgs.url = "github:AtomiCloud/nix-registry";
```
