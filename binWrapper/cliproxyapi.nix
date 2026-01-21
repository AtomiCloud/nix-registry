{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux_amd64";
    aarch64-linux = "linux_arm64";
    aarch64-darwin = "darwin_arm64";
    x86_64-darwin = "darwin_amd64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "b5a2af814e270854f35bb44b2b75d1bdba50c867f0bee732f82d7825406a3fce";
    aarch64-linux = "2ffd1b98f339e7188fecc23fb3e8825ef247c39ade0b60ca680ecec6a39574ed";
    aarch64-darwin = "610e416f8db1a53b3812273ac295110b9d394380a1293ff2cf0e48c7c902a124";
    x86_64-darwin = "a44e23b073bbd66ad5ac43f3632554d26c2babe18065c527a4aeabaf66ba5551";
  }.${system} or throwSystem;
in
let version = "v6.7.16"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "cliproxyapi";
  inherit version;

  src = fetchurl {
    url = "https://github.com/router-for-me/CLIProxyAPI/releases/download/${version}/CLIProxyAPI_${builtins.substring 1 (builtins.stringLength version) version}_${plat}.tar.gz";
    inherit sha256;
  };

  sourceRoot = ".";

  installPhase = ''
    mkdir -p $out/bin
    cp cli-proxy-api $out/bin/
    chmod +x $out/bin/cli-proxy-api
  '';

  meta = with lib; {
    description = "CLI Proxy API - Wrap Gemini CLI, Antigravity, ChatGPT Codex, Claude Code as OpenAI/Gemini/Claude/Codex compatible API service";
    longDescription = ''
      CLIProxyAPI is a proxy server that provides OpenAI/Gemini/Claude/Codex compatible API
      interfaces for CLI tools. It now also supports OpenAI Codex (GPT models) and Claude Code
      via OAuth, allowing you to use local or multi-account CLI access with OpenAI/Gemini/Claude-
      compatible clients and SDKs.

      Features:
      - OpenAI/Gemini/Claude compatible API endpoints for CLI models
      - OpenAI Codex support (GPT models) via OAuth login
      - Claude Code support via OAuth login
      - Qwen Code support via OAuth login
      - iFlow support via OAuth login
      - Streaming and non-streaming responses
      - Function calling/tools support
      - Multimodal input support (text and images)
      - Multiple accounts with round-robin load balancing
      - Simple CLI authentication flows
    '';
    mainProgram = "cli-proxy-api";
    homepage = "https://help.router-for.me/";
    downloadPage = "https://github.com/router-for-me/CLIProxyAPI/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
