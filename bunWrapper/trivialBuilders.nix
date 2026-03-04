{ nixpkgs, bun }:

{
  /*
   * Creates an executable wrapper for a Bun/TypeScript script.
   * The wrapper installs dependencies from bun.lock and creates
   * a bin script that runs the TypeScript entry point.
   *
   * Example:
   * writeBunScriptBin {
   *   name = "my-tool";
   *   version = "1.0.0";
   *   src = ./src;
   *   buildInputs = [ nixpkgs.jq ];
   * }
   */
  writeBunScriptBin = { name, version, src, buildInputs ? [ ] }:
    nixpkgs.stdenv.mkDerivation {
      inherit name version src buildInputs;

      nativeBuildInputs = [ bun ];

      buildPhase = ''
        export HOME=$TMPDIR
        ${bun}/bin/bun install --frozen-lockfile --production
      '';

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
      '';

      meta.mainProgram = name;
    };
}
