{ nixpkgs }:

# `helm schema` is a helm PLUGIN, not a standalone binary: nixpkgs ships it as
# `kubernetes-helmPlugins.helm-schema`, and a plugin is only reachable through a helm
# that has been told where to find it. Invoking it therefore takes two things that
# have to agree — a helm on PATH and a HELM_PLUGINS pointing at the plugin's store
# path — which is exactly the kind of wiring a node should not be hand-rolling in its
# own `nix/packages.nix`. It was hoisted out of the diene helm-wrapper node, where it
# was the last remaining reason that file held a derivation at all.
#
# The wrapper keeps the node's `helm-schema` name, so `$out/bin/helm-schema` is what
# callers get and `scripts/local/generate-chart-schema.sh` keeps working verbatim.
# `writeShellApplication` rather than the `writeShellScriptBin` its sibling wrappers
# use: it takes `runtimeInputs` (so helm is on PATH by construction instead of by a
# hand-written `export PATH=`) and it runs shellcheck over the body at build time,
# which is strictly more than a raw script bin gives us.

let
  makeHelmSchema =
    { helmPackage ? nixpkgs.kubernetes-helm
    , helmSchemaPlugin ? nixpkgs.kubernetes-helmPlugins.helm-schema
    }:
    nixpkgs.writeShellApplication {
      name = "helm-schema";
      runtimeInputs = [ helmPackage helmSchemaPlugin ];
      # HELM_PLUGINS is a path list, not a package: helm reads it to discover plugins
      # and there is no PATH-based fallback, so pointing it at the plugin derivation is
      # the whole mechanism. `exec` keeps the wrapper out of the process tree so exit
      # codes and signals reach the caller unchanged.
      text = ''
        export HELM_PLUGINS="${helmSchemaPlugin}"
        exec helm schema "$@"
      '';
    };
in
nixpkgs.lib.makeOverridable makeHelmSchema { }
