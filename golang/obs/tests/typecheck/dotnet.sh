#!/usr/bin/env bash
# Compiles the generated C# (tests/fixtures/gen/{good,stress}/gen/dotnet/Signals/Signals.g.cs) with the
# dotnet SDK, warnings as errors and all analyzers on. For the good repo: good.cs (a consumer) must build and
# run; every bad-*.cs must fail and the compiler output must name the error code listed in
# typecheck/dotnet/expect.
#   dotnet.sh [--dotnet <dotnet binary>]      (default: dotnet on PATH)
# Runs in the nix check (nixpkgs.dotnet-sdk_10 is an input of the obs-tests derivation); by hand:
# `nix shell nixpkgs#dotnet-sdk_10 -c tests/typecheck/dotnet.sh`.
set -euo pipefail
DOTNET=dotnet
[ "${1:-}" = "--dotnet" ] && DOTNET="$2"
here="$(cd "$(dirname "$0")/.." && pwd)"
g="$here/typecheck/dotnet"
# shellcheck disable=SC1091 # helper is relative to this fixture tree
source "$here/typecheck/stubs.bash"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export HOME="$work/home" DOTNET_CLI_HOME="$work/home" DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
  DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1 MSBUILDDISABLENODEREUSE=1
mkdir -p "$HOME"
build() { "$DOTNET" build "$1" --disable-build-servers -p:UseSharedCompilation=false -nologo -v q 2>&1; }
fail=0
for repo in good stress; do
  d="$work/$repo"
  mkdir -p "$d/Signals" "$d/App"
  cp "$here/fixtures/gen/$repo/gen/dotnet/Signals/Signals.g.cs" "$d/Signals/"
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
  obs_setup_stub dotnet "$d/Signals"
  if out=$(build "$d/Signals/Signals.csproj") && grep -q 'Build succeeded' <<<"$out"; then
    echo "PASS $repo: Signals.g.cs compiles (warnings as errors, all analyzers)"
  else
    echo "FAIL $repo: Signals.g.cs does not compile: $(grep ' error ' <<<"$out" | head -3)"
    fail=1
  fi
done
d="$work/good"
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
cp -f "$g/good.cs" "$d/App/Program.cs"
if out=$(build "$d/App/App.csproj") && run=$("$DOTNET" "$d/App/bin/Debug/net10.0/App.dll" 2>&1) &&
  grep -q 'label shard=3 valid=True' <<<"$run" &&
  grep -q 'checkout.charges checkout.charge.failed error 2026-11-17 provider,outcome,shard,hour' <<<"$run" &&
  grep -q 'client default-valid=False' <<<"$run" &&
  grep -q 'guard: dropped.unknown_event: event "not.in.contract" is not in the generated event table' <<<"$run" &&
  grep -q 'guard-value: dropped.unknown_value: metric "checkout.charges" label provider value "visa" is not one of \[stripe adyen paypal\]' <<<"$run"; then
  echo "PASS good.cs builds, runs: valid calls Ok, nothing dropped, default/null! refused, a reflection-made value refused, the port refuses what its tables lack"
else
  echo "FAIL good.cs: $(grep ' error ' <<<"${out:-}" | head -3) ${run:-}"
  fail=1
fi
while read -r f token; do
  cp -f "$g/$f" "$d/App/Program.cs"
  if out=$(build "$d/App/App.csproj"); then
    echo "FAIL $f compiled but must not"
    fail=1
  elif grep -qF -- "error $token" <<<"$out"; then echo "PASS $f red, names $token"; else
    echo "FAIL $f red but output lacks $token: $(grep ' error ' <<<"$out" | head -2)"
    fail=1
  fi
done <"$g/expect"
cp "$here/fixtures/gen/billing/gen/dotnet/Signals/Signals.g.cs" "$d/Signals/Billing.g.cs"
cp "$g/multi.cs" "$d/App/Program.cs"
if out=$(build "$d/App/App.csproj"); then
  # shellcheck disable=SC2154 # obs_port_cases is supplied by stubs.bash
  for scenario in "${obs_port_cases[@]}" early-real-forgery; do
    if out=$("$DOTNET" "$d/App/bin/Debug/net10.0/App.dll" "$scenario" 2>&1); then echo "$out"; else
      echo "FAIL multi $scenario: $out"
      fail=1
    fi
  done
else
  echo "FAIL multi build: $out"
  fail=1
fi
exit $fail
