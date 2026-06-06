{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-x86_64";
    aarch64-linux = "linux-aarch64";
    x86_64-darwin = "macos-x86_64";
    aarch64-darwin = "macos-aarch64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "99cf4ea2a2a1048d8e9369a6a5a11e5f84ee3f3c706e0bde072f9b2bd44e96ba";
    aarch64-linux = "2327c1de10ecf40e5199c15fdc4c4b3c173735640294e779c635f4c15771e4f6";
    x86_64-darwin = "51751be22f6128229c5dea30dc54e8816b81eb90b53d42b318b11b3afee831d2";
    aarch64-darwin = "e7fed5722af6e14dc668279dd7854109f9778484d48b1a42ead5d2c71b8bb90d";
  }.${system} or throwSystem;
in
let version = "v0.1.1"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "inspect";
  inherit version;

  src = builtins.fetchurl {
    url = "https://github.com/Ataraxy-Labs/inspect/releases/download/${version}/inspect-${plat}";
    inherit sha256;
  };

  nativeBuildInputs = lib.optionals stdenv.hostPlatform.isLinux [
    autoPatchelfHook
  ];

  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [
    openssl
    zlib
    stdenv.cc.cc.lib
  ] ++ lib.optionals stdenv.hostPlatform.isDarwin [
    openssl
  ];

  unpackPhase = "true";

  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp $src $out/bin/inspect
    chmod +x $out/bin/inspect
  '';

  postFixup = lib.optionalString stdenv.hostPlatform.isDarwin ''
    install_name_tool -change /opt/homebrew/opt/openssl@3/lib/libssl.3.dylib ${openssl.out}/lib/libssl.3.dylib $out/bin/inspect
    install_name_tool -change /opt/homebrew/opt/openssl@3/lib/libcrypto.3.dylib ${openssl.out}/lib/libcrypto.3.dylib $out/bin/inspect
  '';

  meta = with lib; {
    description = "Entity-level code review CLI for Git with graph-based risk scoring";
    mainProgram = "inspect";
    homepage = "https://inspect.ataraxy-labs.com";
    downloadPage = "https://github.com/Ataraxy-Labs/inspect/releases";
    license = licenses.fsl11Asl20;
    platforms = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
  };
})
