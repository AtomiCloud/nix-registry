{ nixpkgs, buildBunCli, ul }:

(buildBunCli {
  name = "doccheck";
  version = "0.1.0";
  src = nixpkgs.lib.cleanSource ./.;
  entrypoint = "src/main.ts";
  depsHash = "sha256-TPjJv1+Mk4fEpaZy92EYw4HgUugnuoQJKfW5PA6BXX8=";
  meta = {
    description = "Documentation checks and generated maps through Durian Hypermedia";
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
}).overrideAttrs (old: {
  nativeBuildInputs = old.nativeBuildInputs ++ [ nixpkgs.makeWrapper ];
  nativeCheckInputs = [ nixpkgs.git nixpkgs.universal-ctags ul ];
  doCheck = true;
  checkPhase = ''
    runHook preCheck
    bun test src
    runHook postCheck
  '';
  postInstall = ''
    wrapProgram "$out/bin/doccheck" --prefix PATH : ${nixpkgs.lib.makeBinPath [ nixpkgs.git nixpkgs.universal-ctags ul ]}
  '';
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    test "$("$out/bin/doccheck" --version)" = "0.1.0"
    "$out/bin/doccheck" --help
    mkdir -p pipe-test/docs
    git -C pipe-test init -q
    bun -e 'await Bun.write("pipe-test/docs/doccheck.yaml", "pageOrder: B\nllmView: build\n"); await Bun.write("pipe-test/docs/outline.yaml", "repo:\n  - page: sample.md\n    type: feature\n    rank: core\n    learns: A sample.\n"); await Bun.write("pipe-test/docs/sample.md", "---\ntitle: Sample\nconcept: sample\ndescription: A sample.\nnamespaces: []\ntype: feature\nstub: true\nrelated:\n" + Array.from({length: 200}, (_,i) => "  - absent-" + i + ".md\n").join("") + "---\n\nA sample.\n");'
    "$out/bin/doccheck" check pipe-test/docs --format json > pipe-test/direct.json || test "$?" = 1
    "$out/bin/doccheck" check pipe-test/docs --format json | cat > pipe-test/piped.json || test "$?" = 1
    cmp pipe-test/direct.json pipe-test/piped.json
    bun -e 'const text = await Bun.file("pipe-test/piped.json").text(); if (text.length <= 8192) throw new Error("pipe fixture too small"); JSON.parse(text);'
    runHook postInstallCheck
  '';
})
