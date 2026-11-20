{ nixpkgs, buildBunCli }:

# The manifest pins durian-hypermedia to v0.2.0; bun.lock pins its tag object.
# depsHash verifies the complete dependency tree, including that source.
(buildBunCli {
  name = "dh";
  version = "0.2.0";
  src = nixpkgs.lib.cleanSource ../doccheck;
  entrypoint = "src/dh-cli.ts";
  depsHash = "sha256-TPjJv1+Mk4fEpaZy92EYw4HgUugnuoQJKfW5PA6BXX8=";
  meta = {
    description = "Durian Hypermedia LLM views, parsed JSON, translation stamps and token estimates";
    homepage = "https://github.com/ProtoDurian/durian-hypermedia";
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
}).overrideAttrs (old: {
  nativeBuildInputs = old.nativeBuildInputs ++ [ nixpkgs.makeWrapper ];
  postInstall = ''
    wrapProgram "$out/bin/dh" --prefix PATH : ${nixpkgs.lib.makeBinPath [ nixpkgs.git ]}
  '';
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    test "$("$out/bin/dh" --version)" = "0.2.0"
    "$out/bin/dh" --help
    runHook postInstallCheck
  '';
})
