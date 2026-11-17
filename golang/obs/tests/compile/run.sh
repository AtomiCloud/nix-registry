#!/usr/bin/env bash
# Compile check of the generated C# and Dart (needs the dotnet SDK and the dart SDK on PATH; the
# nix check sandbox has neither, so CI runs this as its own step):
#
#   nix shell nixpkgs#dotnet-sdk_10 nixpkgs#dart -c bash tests/compile/run.sh [dotnet|dart]
#
# For each fixture consumer repo under tests/fixtures/gen (good, stress):
#   - the generated file compiles with warnings as errors (C#: all analyzers; Dart: strict analysis)
#     and, for `dart`, is already what `dart format` would write;
# and for the `good` repo:
#   - the good consumer builds and runs;
#   - every planted bad consumer in tests/compile/<lang>/bad fails, with the compiler error named in
#     its first line (`// expect: <code>`).
# Exit 0 when all of that holds.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
fx="$here/../fixtures/gen"
want="${1:-all}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export HOME="$work/home" DOTNET_CLI_HOME="$work/home" DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
  DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1 MSBUILDDISABLENODEREUSE=1 DART_SUPPRESS_ANALYTICS=true
mkdir -p "$HOME"
pass=0
fail=0
ok() {
  echo "PASS $*"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL $*"
  fail=$((fail + 1))
}

dotnet_build() { # <project dir>
  dotnet build "$1" --disable-build-servers -p:UseSharedCompilation=false -nologo -v q 2>&1
}

check_dotnet() {
  command -v dotnet >/dev/null || {
    bad "dotnet: no dotnet SDK on PATH"
    return
  }
  for repo in good stress; do
    d="$work/cs-$repo"
    mkdir -p "$d/Signals" "$d/App"
    cp "$fx/$repo/gen/dotnet/Signals/Signals.g.cs" "$d/Signals/"
    cat >"$d/Signals/Signals.csproj" <<'XML'
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
    <Nullable>enable</Nullable>
    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>
    <GenerateDocumentationFile>true</GenerateDocumentationFile>
    <EnforceCodeStyleInBuild>true</EnforceCodeStyleInBuild>
    <AnalysisMode>All</AnalysisMode>
  </PropertyGroup>
</Project>
XML
    out="$(dotnet_build "$d/Signals/Signals.csproj")"
    if echo "$out" | grep -q 'Build succeeded' && ! echo "$out" | grep -q ' error '; then
      ok "dotnet $repo: the generated C# compiles (warnings as errors, all analyzers)"
    else
      bad "dotnet $repo: Signals.g.cs does not compile: $(echo "$out" | grep ' error ' | head -3)"
    fi
  done
  d="$work/cs-good"
  cat >"$d/App/App.csproj" <<'XML'
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net10.0</TargetFramework>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>
  </PropertyGroup>
  <ItemGroup><ProjectReference Include="../Signals/Signals.csproj" /></ItemGroup>
</Project>
XML
  cp "$here/dotnet/consumer.cs" "$d/App/Program.cs"
  out="$(dotnet_build "$d/App/App.csproj")"
  if echo "$out" | grep -q 'Build succeeded' && run="$(dotnet "$d/App/bin/Debug/net10.0/App.dll" 2>&1)" &&
    echo "$run" | grep -q 'label shard=3 valid=True' && echo "$run" | grep -q 'checkout.charges checkout.charge.failed error 2026-11-17 provider,outcome,shard,hour' &&
    echo "$run" | grep -q 'client default-valid=False'; then
    ok "dotnet good consumer builds and runs (default(DurianSignalInt) is not Valid)"
  else
    bad "dotnet good consumer: $(echo "$out" | grep ' error ' | head -3) ${run:-}"
  fi
  for f in "$here"/dotnet/bad/*.cs; do
    code="$(sed -n '1s#^// expect: ##p' "$f")"
    cp "$f" "$d/App/Program.cs"
    out="$(dotnet_build "$d/App/App.csproj")"
    if echo "$out" | grep -q "error $code"; then
      ok "dotnet planted $(basename "$f") fails with $code"
    else
      bad "dotnet planted $(basename "$f") did not fail with $code: $(echo "$out" | grep -E 'error|Build succeeded' | head -2)"
    fi
  done
}

check_dart() {
  command -v dart >/dev/null || {
    bad "dart: no dart SDK on PATH"
    return
  }
  for repo in good stress; do
    pkg=checkout_signals
    [ "$repo" = stress ] && pkg=pay_signals
    d="$work/dt-$repo"
    mkdir -p "$d/lib/src/signals" "$d/bin" "$d/bad"
    cp "$fx/$repo/gen/dart/lib/src/signals/signals.g.dart" "$d/lib/src/signals/"
    printf 'name: %s\npublish_to: none\nenvironment:\n  sdk: ^3.7.0\n' "$pkg" >"$d/pubspec.yaml"
    printf 'analyzer:\n  language:\n    strict-casts: true\n    strict-inference: true\n    strict-raw-types: true\n' >"$d/analysis_options.yaml"
    (cd "$d" && dart pub get --offline >/dev/null 2>&1)
    out="$(cd "$d" && dart analyze 2>&1)"
    if echo "$out" | grep -q 'No issues found'; then
      ok "dart $repo: the generated Dart analyzes clean (strict casts, inference, raw types)"
    else
      bad "dart $repo: signals.g.dart does not analyze clean: $(echo "$out" | head -4)"
    fi
    if (cd "$d" && dart format --output=none --set-exit-if-changed lib >/dev/null 2>&1); then
      ok "dart $repo: the generated Dart is what dart format writes"
    else
      bad "dart $repo: dart format would change signals.g.dart"
    fi
  done
  d="$work/dt-good"
  cp "$here/dart/consumer.dart" "$d/bin/app.dart"
  out="$(cd "$d" && dart analyze 2>&1)"
  if echo "$out" | grep -q 'No issues found' && run="$(cd "$d" && dart run bin/app.dart 2>&1)" &&
    echo "$run" | grep -q 'checkout.charges checkout.charge.failed error' && echo "$run" | grep -q '2026-11-17'; then
    ok "dart good consumer analyzes clean and runs"
  else
    bad "dart good consumer: $(echo "$out" | head -3) ${run:-}"
  fi
  for f in "$here"/dart/bad/*.dart; do
    code="$(sed -n '1s#^// expect: ##p' "$f")"
    cp "$f" "$d/bad/case.dart"
    out="$(cd "$d" && dart analyze bad/case.dart 2>&1)"
    if echo "$out" | grep -E "error .*$code|$code.*error" >/dev/null; then
      ok "dart planted $(basename "$f") fails with $code"
    else
      bad "dart planted $(basename "$f") did not fail with $code: $(echo "$out" | head -3)"
    fi
  done
}

case "$want" in
all)
  check_dotnet
  check_dart
  ;;
dotnet) check_dotnet ;;
dart) check_dart ;;
*)
  echo "usage: run.sh [dotnet|dart]"
  exit 2
  ;;
esac
echo "passed $pass, failed $fail"
[ "$fail" -eq 0 ]
