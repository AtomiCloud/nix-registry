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
    mkdir -p pipe-test/docs
    bun -e 'await Bun.write("pipe-test/docs/large.md", "---\ntitle: Large\nconcept: large\ndescription: A large page.\nnamespaces: []\ntype: feature\n---\n\n" + "A paragraph that must reach the renderer intact.\n\n".repeat(1000));'
    "$out/bin/dh" json pipe-test/docs/large.md > pipe-test/direct.json
    "$out/bin/dh" json pipe-test/docs/large.md | cat > pipe-test/piped.json
    cmp pipe-test/direct.json pipe-test/piped.json
    bun -e 'const text = await Bun.file("pipe-test/piped.json").text(); if (text.length <= 8192) throw new Error("pipe fixture too small"); JSON.parse(text);'
    runHook postInstallCheck
  '';
})
