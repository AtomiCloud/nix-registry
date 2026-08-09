{ nixpkgs, atomiutils }:
# The PATH a fleet node's `scripts/validate/*.sh` hooks run under.
#
# Every one of those hooks is invoked through the same shape in a node's
# `nix/pre-commit.nix`:
#
#   validator = command:
#     "${bash}/bin/bash -c 'export PATH=${validator-runtime}/bin; exec bash ${command}'";
#
# The point of the `export PATH=` is that a validator sees a CLOSED tool set: it
# cannot silently start depending on whatever the developer happens to have
# installed. That makes the runtime's contents load-bearing, and it is why this
# is a `buildEnv` rather than a plain list — `buildEnv` ERRORS on a PATH
# collision where `mkShell` silently orders one.
#
# Measured on the 57 fleet node refs in AtomiCloud/diene.all: 44 carry a
# `workspace-validator-runtime` under exactly that name. Of those, 4 are already
# literally `[ atomiutils git ]`, and 16 more spell out
# `[ bash git jq ripgrep yq-go coreutils findutils gnugrep gnused ]`, which is a
# strict subset of what `atomiutils` provides plus `git` — so 20 nodes are
# carrying an identical derivation each. The remaining 24 add node-specific
# tools (bun, dart, flutter, helm, kubeconform, kyverno, gitlint, util-linux)
# and are genuinely parameterised on the node; they are NOT this package's
# business and stay node-side, the same ruling that keeps
# `go-base-lint-runtime` / `go-base-deadcode-runtime` node-side.
#
# Deliberately NOT `lib.makeOverridable` with an `extraPaths` knob. The sibling
# wrappers here are overridable because they have a real parameter (which helm,
# which .NET SDK); this one has none — that is the measured finding it exists
# on. Adding a knob would invite the parameterised nodes to adopt it via
# `.override`, which is the exact construct the merger refuses and the reason
# any of this is being hoisted. A node that needs more tools keeps its own
# buildEnv until there is a ruling that says otherwise.
nixpkgs.buildEnv {
  name = "workspace-validator-runtime";
  paths = [
    atomiutils
    nixpkgs.git
  ];
}
