{ nixpkgs, bun, trivialBuilders }:

let
  # package.json is the single source of the version: the CLI reads it at
  # runtime, so a version written twice could disagree with itself.
  version = (builtins.fromJSON (builtins.readFile ./package.json)).version;

  # The installCheck below runs with the build PATH, so every binary it calls is
  # named by its store path. A guard whose own tools are assumed rather than
  # declared can fail for a reason that has nothing to do with what it asserts,
  # and a failing integrity guard is the one message that must never be
  # ambiguous.
  coreutils = "${nixpkgs.coreutils}/bin";
in
(trivialBuilders.writeBunScriptBin {
  inherit version;
  name = "resolver-smoke";
  src = ./.;

  # No runtimeInputs on purpose. Every probe runs in-process against the
  # VENDORED merger, which `writeBunScriptBin` copies into
  # $out/lib/resolver-smoke/vendor/ along with the rest of `src`. The check
  # therefore shells out to nothing and never reaches the network, which is the
  # property that lets it run as a pre-push gate.
}).overrideAttrs (_: {
  # resolver-smoke's whole claim is that it ran THE PUBLISHED merger. That claim
  # rests on the vendored bundle being the published bytes, so the digest is
  # asserted twice: here, so a bad bundle cannot even be built, and again at
  # runtime in src/vendor.ts, so a bundle substituted after the build cannot be
  # used. Neither assertion is redundant — they refuse at different moments.
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck

    bundle="$out/lib/resolver-smoke/vendor/nix.mjs"
    record="$out/lib/resolver-smoke/vendor/SHA256"

    if [ ! -f "$bundle" ]; then
      echo "ERROR: resolver-smoke shipped no vendored merger." >&2
      echo "  expected: $bundle" >&2
      echo "The check is hermetic only because the published atomi/nix@2 bundle" >&2
      echo "travels inside the derivation. Without it there is nothing to run the" >&2
      echo "probes against, and the tool would have to fetch a resolver at hook" >&2
      echo "time. Restore vendor/nix.mjs; do not make the bundle optional." >&2
      exit 1
    fi

    if [ ! -f "$record" ]; then
      echo "ERROR: resolver-smoke shipped a vendored merger with no recorded digest." >&2
      echo "  expected: $record" >&2
      echo "vendor/SHA256 is the ONE place the digest is written. An unrecorded" >&2
      echo "bundle cannot be told apart from a substituted one, so an absent" >&2
      echo "record is not a weaker check — it is no check at all." >&2
      exit 1
    fi

    # The record is read out of the OUTPUT rather than with builtins.readFile.
    # Writing the digest into this expression as well would make vendor/SHA256
    # and the derivation two constants that can drift apart, and the one this
    # phase compared against would stop being the one src/vendor.ts reads.
    # `cut -d' ' -f1` takes the first field, so a bare digest line and
    # `sha256sum` output ("<digest>  <path>") are both read the same way.
    expected="$(${coreutils}/head -n 1 "$record" | ${coreutils}/cut -d' ' -f1 | ${coreutils}/tr -d '[:space:]')"

    # Validated before it is compared: an empty or malformed record would
    # otherwise "mismatch" and send the reader looking at the bundle, which is
    # the wrong file entirely.
    notHex="$(printf '%s' "$expected" | ${coreutils}/tr -d '0-9a-f')"
    length="$(printf '%s' "$expected" | ${coreutils}/wc -c)"
    if [ -n "$notHex" ] || [ "$length" -ne 64 ]; then
      echo "ERROR: vendor/SHA256 does not hold a sha256 digest." >&2
      echo "  read: '$expected'" >&2
      echo "It must be the 64 lowercase hex characters sha256sum prints for" >&2
      echo "vendor/nix.mjs, optionally followed by the file name." >&2
      exit 1
    fi

    actual="$(${coreutils}/sha256sum "$bundle" | ${coreutils}/cut -d' ' -f1)"

    if [ "$actual" != "$expected" ]; then
      echo "ERROR: the vendored atomi/nix@2 merger is not the bytes resolver-smoke recorded." >&2
      echo "  recorded (vendor/SHA256):     $expected" >&2
      echo "  built    (vendor/nix.mjs): $actual" >&2
      echo >&2
      echo "resolver-smoke proves a repository is resolver-friendly by RUNNING the" >&2
      echo "published merger over it. If the bundle is not the published bytes," >&2
      echo "every green result the tool has produced is about some other program." >&2
      echo >&2
      echo "A formatter is the likeliest cause: prettier rewrites '*.mjs', which" >&2
      echo "is why the bundle is listed in the repository's .prettierignore." >&2
      echo "If the bundle was refreshed deliberately, re-record it:" >&2
      echo "  ./bunWrapper/resolver-smoke/scripts/refresh-vendor.sh <published-bundle-path-or-url>" >&2
      echo "Do not hand-edit vendor/SHA256 to match whatever is on disk; that" >&2
      echo "turns the guard into a rubber stamp." >&2
      exit 1
    fi

    echo "vendored atomi/nix@2 merger matches its recorded digest: $actual"

    runHook postInstallCheck
  '';
})
