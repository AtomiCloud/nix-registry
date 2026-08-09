{ nixpkgs, bundles, unions }:
# The contract for every tool aggregate in this registry.
#
# An aggregate's value IS its bin/ list: fleet nodes compose bundles by axis, so
# a tool silently entering or leaving one (an upstream nixpkgs package growing a
# second binary, a `cp` line quietly added) changes what every consumer gets
# with no signal in the diff. This derivation fails the build unless each
# bundle's bin/ matches the list declared below exactly, which makes any such
# change a red diff on this file.
#
# To change a bundle: change the derivation AND this list, in the same commit.
let
  pkgs = nixpkgs;
  inherit (pkgs) lib;

  # nixpkgs builds pkgs.docker client-only off Linux
  # (clientOnly ? !stdenv.hostPlatform.isLinux), so the daemon binaries exist on
  # Linux only. Everything else in these bundles is a single static CLI with the
  # same bin/ on all three supported systems.
  dockerBins = [ "docker" ] ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [
    "dockerd"
    "dockerd-rootless"
  ];

  # Axis-pure bundles: the declared bin/ list of each.
  axis = {
    infrautils-core = [ "garden" "mirrord" "tilt" "tofu" ];
    infrautils-docker = dockerBins;
    infrautils-k8s = [ "helm" "k3d" "kubectl" "kubectx" "kubens" ];

    infralint-core = [
      "kubeconform"
      "kyverno"
      "terraform-docs"
      "tflint"
      "tfsec"
      "tfsec-checkgen"
      "tfsec-docs"
    ];
    infralint-docker = [ "hadolint" "skopeo" ];
    infralint-helm = [ "helm-docs" "helmlint" ];
  };

  # Full bundles: kept as they always were, and asserted to be exactly the union
  # of their axis slices. This is what stops the split from drifting away from
  # the aggregate it was cut out of.
  composition = {
    infrautils = [ "infrautils-core" "infrautils-docker" "infrautils-k8s" ];
    infralint = [ "infralint-core" "infralint-docker" "infralint-helm" ];
  };

  unionOf = parts: lib.concatMap (part: axis.${part}) parts;

  contract = axis // lib.mapAttrs (_: unionOf) composition;

  sortBins = bins: lib.sort (a: b: a < b) (lib.unique bins);

  manifestOf = name: bins:
    pkgs.writeText "${name}.bins" (lib.concatMapStrings (bin: bin + "\n") (sortBins bins));

  verify = name: bins: ''
    echo "==> ${name}"
    ls -1 "${bundles.${name}}/bin" | LC_ALL=C sort > "${name}.actual"
    if ! diff -u "${manifestOf name bins}" "${name}.actual"; then
      echo "" >&2
      echo "bundle '${name}' does not match its declared bin/ contract." >&2
      echo "'-' lines are declared but missing, '+' lines appeared unannounced." >&2
      echo "Fix the derivation, or declare the change in binWrapper/bundleContract.nix." >&2
      exit 1
    fi
    cp "${name}.actual" "$out/share/tool-bundle-contract/${name}.bins"
  '';

  # Union bundles: aggregates whose bin/ is not a hand-written list but the union
  # of the bin/ of the packages they compose. `workspace-validator-runtime` is
  # `atomiutils` + `git`, and `atomiutils` alone is ~200 binaries copied out of a
  # dozen upstream packages — transcribing that here would be a manifest nobody
  # can review and that goes stale on the next channel bump.
  #
  # So the contract is stated the way the derivation is: EXACTLY the union of its
  # parts, computed from those parts at build time. That still fails the build on
  # the change this is guarding against — a third path quietly appended to
  # `paths`, or a part swapped out — because the expected side is derived from the
  # DECLARED parts below, not from the bundle itself.
  #
  # `requires` is the second half, and it is the half consumers actually depend
  # on: a validator hook runs with `PATH=${bundle}/bin` and nothing else, so a
  # tool leaving one of the upstream packages turns every hook that calls it into
  # a `command not found` at commit time. These are asserted present and
  # executable by name.
  verifyUnion = name: { parts, requires }: ''
    echo "==> ${name} (union of ${toString (builtins.length parts)} parts)"
    : > "${name}.parts"
    ${lib.concatMapStrings (part: ''
      ls -1 "${part}/bin" >> "${name}.parts"
    '') parts}
    LC_ALL=C sort -u "${name}.parts" > "${name}.expected"
    ls -1 "${unions.${name}.bundle}/bin" | LC_ALL=C sort > "${name}.actual"
    if ! diff -u "${name}.expected" "${name}.actual"; then
      echo "" >&2
      echo "bundle '${name}' is not exactly the union of its declared parts." >&2
      echo "'-' lines are in a part but missing from the bundle, '+' lines are in" >&2
      echo "the bundle but in no declared part." >&2
      echo "Fix the derivation, or declare the change in binWrapper/bundleContract.nix." >&2
      exit 1
    fi
    for tool in ${lib.concatStringsSep " " requires}; do
      if [ ! -x "${unions.${name}.bundle}/bin/$tool" ]; then
        echo "" >&2
        echo "bundle '${name}' is missing required tool '$tool'." >&2
        echo "Every consumer of this bundle runs with PATH set to it and nothing" >&2
        echo "else, so a missing tool is a runtime failure in every hook that" >&2
        echo "calls it. Restore it, or drop it from the requires list in" >&2
        echo "binWrapper/bundleContract.nix and say why." >&2
        exit 1
      fi
    done
    echo "    ${toString (builtins.length requires)} required tools present"
    cp "${name}.actual" "$out/share/tool-bundle-contract/${name}.bins"
  '';
in

pkgs.runCommand "tool-bundle-contract"
{
  nativeBuildInputs = [ pkgs.coreutils pkgs.diffutils ];
} ''
  mkdir -p $out/share/tool-bundle-contract
  ${lib.concatStrings (lib.mapAttrsToList verify contract)}
  ${lib.concatStrings (lib.mapAttrsToList (name: u: verifyUnion name { inherit (u) parts requires; }) unions)}
  echo "🎉 all ${toString (builtins.length (builtins.attrNames contract) + builtins.length (builtins.attrNames unions))} tool bundles match their declared contract"
''
