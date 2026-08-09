{ nixpkgs, bundles }:
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
in

pkgs.runCommand "tool-bundle-contract"
{
  nativeBuildInputs = [ pkgs.coreutils pkgs.diffutils ];
} ''
  mkdir -p $out/share/tool-bundle-contract
  ${lib.concatStrings (lib.mapAttrsToList verify contract)}
  echo "🎉 all ${toString (builtins.length (builtins.attrNames contract))} tool bundles match their declared contract"
''
