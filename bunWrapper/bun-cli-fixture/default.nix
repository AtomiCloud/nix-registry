{ buildBunCli }:

# Smoke fixture for buildBunCli: a tiny CLI with one real dependency, so the
# fixed-output deps derivation, the compile and the binary are all exercised.
(buildBunCli {
  name = "bun-cli-fixture";
  version = "0.1.0";
  src = ./.;
  entrypoint = "index.ts";
  depsHash = "sha256-6kAV5zIytffQHoUXBG2QDL1beCb6eaeouoxSFkCcqo0=";
}).overrideAttrs (_: {
  # Runs the compiled binary at build time. The greeting is computed by the
  # bundled dependency, so a missing or broken node_modules fails the build.
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    [ "$("$out/bin/bun-cli-fixture" --version)" = "0.1.0" ]
    [ "$("$out/bin/bun-cli-fixture")" = "hello from bun-cli-fixture: 3 is odd" ]
    runHook postInstallCheck
  '';
})
