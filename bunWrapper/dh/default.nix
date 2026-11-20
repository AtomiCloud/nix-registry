{ nixpkgs, buildBunCli }:

# The manifest pins durian-hypermedia to v0.1.0; bun.lock pins its tag object.
# depsHash verifies the complete dependency tree, including that source.
(buildBunCli {
  name = "dh";
  version = "0.1.0";
  src = nixpkgs.lib.cleanSource ../doccheck;
  entrypoint = "src/dh-cli.ts";
  depsHash = "sha256-GFqO55NDLyD4qRae2Zn9fnw5nJIJ7FUnHRk983pbskQ=";
  meta = {
    description = "Durian Hypermedia LLM views, parsed JSON, translation stamps and token estimates";
    homepage = "https://github.com/ProtoDurian/durian-hypermedia";
    platforms = nixpkgs.lib.platforms.unix;
  };
}).overrideAttrs (_: {
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    test "$("$out/bin/dh" --version)" = "0.1.0"
    "$out/bin/dh" --help
    runHook postInstallCheck
  '';
})
