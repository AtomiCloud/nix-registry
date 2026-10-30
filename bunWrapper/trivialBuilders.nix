{ nixpkgs, bun }:

{
  /*
   * Creates an executable wrapper for a Bun/TypeScript script.
   * The wrapper installs dependencies from bun.lock and creates
   * a bin script that runs the TypeScript entry point.
   *
   * `runtimeInputs` are prefixed onto PATH for the wrapped script, so a binary
   * the tool shells out to is in its own runtime closure rather than assumed to
   * be on the consumer's PATH. `buildInputs` stays for build-time needs only.
   *
   * `depsHash` is the fixed-output hash of the package's production
   * `node_modules`. A package with a `bun.lock` MUST set it: the dependency
   * fetch is the only step that needs the network, and a sandboxed build has
   * none, so it runs in its own fixed-output derivation (the one kind nix lets
   * reach the network) and the package build copies the result in. Derive it by
   * setting `depsHash = nixpkgs.lib.fakeHash;` and taking the hash nix reports.
   * A package with no dependencies (no `bun.lock`) leaves it unset.
   *
   * Example:
   * writeBunScriptBin {
   *   name = "my-tool";
   *   version = "1.0.0";
   *   src = ./src;
   *   depsHash = "sha256-...";
   *   buildInputs = [ nixpkgs.jq ];
   *   runtimeInputs = [ nixpkgs.git ];
   * }
   */
  writeBunScriptBin = { name, version, src, depsHash ? null, buildInputs ? [ ], runtimeInputs ? [ ] }:
    # Without this, a package that gains dependencies but no depsHash would run
    # `bun install` inside the sandbox again: it builds wherever the sandbox is
    # off or the output is already cached, and fails only on a clean runner.
    assert nixpkgs.lib.assertMsg (depsHash != null || !(builtins.pathExists (src + "/bun.lock")))
      "writeBunScriptBin: ${name} has a bun.lock but no depsHash. Set depsHash = nixpkgs.lib.fakeHash; and use the hash nix reports.";
    let
      # Production deps only, as the package build always installed: dev
      # dependencies never reach the wrapper, and leaving them out keeps the
      # tree free of platform-specific binaries so one hash covers every system.
      deps = nixpkgs.stdenv.mkDerivation {
        pname = "${name}-deps";
        inherit version src;
        nativeBuildInputs = [ bun ];
        dontConfigure = true;
        buildPhase = ''
          export HOME=$TMPDIR
          ${bun}/bin/bun install --frozen-lockfile --production --no-progress
        '';
        installPhase = ''
          mkdir -p $out
          cp -r node_modules $out/node_modules
        '';
        # A fixed-output path may not reference the store, and patchShebangs
        # would make it; the package build's own fixup patches these files once
        # they are copied into its output, exactly as it did before.
        dontFixup = true;
        outputHashMode = "recursive";
        outputHashAlgo = "sha256";
        outputHash = depsHash;
      };
    in
    nixpkgs.stdenv.mkDerivation {
      inherit name version src buildInputs;

      nativeBuildInputs = [ bun ];

      # The copy is made writable because the package's own fixup rewrites
      # shebangs inside node_modules, and store paths are read-only.
      buildPhase =
        if depsHash == null then ''
          export HOME=$TMPDIR
          ${bun}/bin/bun install --frozen-lockfile --production
        '' else ''
          export HOME=$TMPDIR
          cp -r ${deps}/node_modules ./node_modules
          chmod -R u+w node_modules
        '';

      # The PATH line is APPENDED after the wrapper is written rather than
      # interpolated into it, so a package with no runtimeInputs produces a
      # byte-identical installPhase — and therefore the same derivation — as it
      # did before this option existed. Interpolating an empty string into the
      # heredoc would leave a blank line behind and rebuild every bun package in
      # the registry for no change in what they do.
      installPhase = ''
        # Copy source files and node_modules to $out/lib for runtime access
        mkdir -p $out/lib/${name}
        cp -r . $out/lib/${name}/

        # Create the wrapper script that references the installed lib directory
        mkdir -p $out/bin
        cat > $out/bin/${name} << SCRIPT
        #!/bin/sh
        exec ${bun}/bin/bun run $out/lib/${name}/index.ts "\$@"
        SCRIPT
        chmod +x $out/bin/${name}
      '' + nixpkgs.lib.optionalString (runtimeInputs != [ ]) ''

        # `$PATH` is single-quoted here so it stays an expansion in the generated
        # script instead of being resolved to the builder's PATH.
        sed -i '1a export PATH=${nixpkgs.lib.makeBinPath runtimeInputs}:$PATH' $out/bin/${name}
      '';

      meta.mainProgram = name;
    };
}
