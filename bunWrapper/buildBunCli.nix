{ nixpkgs, defaultBun }:

/*
 * Builds a Bun project into a single self-contained executable with
 * `bun build --compile`. Exported to consumers as `lib.<system>.buildBunCli`.
 *
 * Two derivations, because the dependency fetch is the only step that needs the
 * network and a sandboxed build has none: the fetch runs in a fixed-output
 * derivation (the one kind nix lets reach the network), and the compile step
 * copies its result in.
 *
 * Example:
 * buildBunCli {
 *   name = "my-cli";
 *   version = "1.0.0";
 *   src = ./.;
 *   entrypoint = "src/main.ts";
 *   depsHash = "sha256-...";
 * }
 *
 * `depsHash` is the fixed-output hash of the production `node_modules`. Derive
 * it by setting `depsHash = nixpkgs.lib.fakeHash;` and taking the hash nix
 * reports. It is one string for every system when the production tree is the
 * same on all of them (production-only installs keep it so, as dev dependencies
 * are where platform-specific binaries live). Otherwise it is an attrset keyed
 * by system: `{ x86_64-linux = "sha256-..."; aarch64-darwin = "sha256-..."; }`.
 *
 * Optional:
 *   bun         the bun package to install and compile with (default: the
 *               registry's bun). Changing it changes the compiled runtime.
 *   production  install only `dependencies` (default: true).
 *   buildArgs   extra flags appended to `bun build` (default: "").
 *   meta        merged over `{ mainProgram = name; }`.
 */
{ name
, version
, src
, entrypoint
, depsHash
, bun ? defaultBun
, production ? true
, buildArgs ? ""
, meta ? { }
}:
let
  system = nixpkgs.stdenv.hostPlatform.system;
  outputHash =
    if builtins.isAttrs depsHash then
      depsHash.${system} or (throw "buildBunCli: ${name} has no depsHash for ${system}.")
    else depsHash;

  # A fixed-output path is named by its name and hash alone, so if bun.lock
  # changed under an unchanged depsHash, nix would reuse the OLD node_modules
  # without running the install. Putting a digest of the manifest and lockfile
  # in the name gives every change to them a new path, so a stale depsHash fails
  # as a hash mismatch instead of shipping the old dependencies. bunfig.toml is
  # in it too because its [install] settings change the tree.
  bunfig = src + "/bunfig.toml";
  manifestDigest = builtins.substring 0 12 (builtins.hashString "sha256"
    (builtins.readFile (src + "/package.json") + builtins.readFile (src + "/bun.lock")
      + nixpkgs.lib.optionalString (builtins.pathExists bunfig) (builtins.readFile bunfig)));

  deps = nixpkgs.stdenv.mkDerivation {
    pname = "${name}-deps-${manifestDigest}";
    inherit version src;
    nativeBuildInputs = [ bun ];
    dontConfigure = true;
    buildPhase = ''
      export HOME="$TMPDIR"
      bun install --frozen-lockfile --no-progress ${nixpkgs.lib.optionalString production "--production"}
    '';
    installPhase = ''
      mkdir -p "$out"
      cp -r node_modules "$out/node_modules"
    '';
    # A fixed-output path may not reference the store, and fixup's patchShebangs
    # would make it.
    dontFixup = true;
    outputHashMode = "recursive";
    outputHashAlgo = "sha256";
    inherit outputHash;
  };
in
nixpkgs.stdenv.mkDerivation {
  pname = name;
  inherit version src;
  nativeBuildInputs = [ bun ];
  dontConfigure = true;
  buildPhase = ''
    export HOME="$TMPDIR"
    cp -r ${deps}/node_modules ./node_modules
    chmod -R u+w node_modules
    bun build "./${entrypoint}" --compile --outfile "${name}" ${buildArgs}
  '';
  installPhase = ''
    mkdir -p "$out/bin"
    cp "${name}" "$out/bin/${name}"
  '';
  # Bun --compile emits a self-contained executable; fixup's strip would corrupt
  # the bytecode blob embedded in it.
  dontFixup = true;
  meta = { mainProgram = name; } // meta;
}
