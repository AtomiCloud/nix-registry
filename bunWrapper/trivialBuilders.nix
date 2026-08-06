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
   * Example:
   * writeBunScriptBin {
   *   name = "my-tool";
   *   version = "1.0.0";
   *   src = ./src;
   *   buildInputs = [ nixpkgs.jq ];
   *   runtimeInputs = [ nixpkgs.git ];
   * }
   */
  writeBunScriptBin = { name, version, src, buildInputs ? [ ], runtimeInputs ? [ ] }:
    nixpkgs.stdenv.mkDerivation {
      inherit name version src buildInputs;

      nativeBuildInputs = [ bun ];

      buildPhase = ''
        export HOME=$TMPDIR
        ${bun}/bin/bun install --frozen-lockfile --production
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
