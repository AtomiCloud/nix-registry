#!/usr/bin/env bash
# Source this in compiler checks. These are minimal APIs assumed from the goals,
# not published libraries: checks prove compilation against a stub only.
obs_setup_stub() {
  local lang="$1" d="$2"
  local stubs
  stubs="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  case "$lang" in
  go)
    mkdir -p "$d/interfaces-stub"
    cp -r --no-preserve=mode "$stubs/go/stub/." "$d/interfaces-stub/"
    cat >>"$d/go.mod" <<'MOD'

require github.com/ProtoDurian/diene.go-interfaces v0.0.0
replace github.com/ProtoDurian/diene.go-interfaces => ./interfaces-stub
MOD
    ;;
  bun)
    mkdir -p "$d/node_modules/@atomicloud/diene.interfaces"
    cp -r --no-preserve=mode "$stubs/bun/stub/." "$d/node_modules/@atomicloud/diene.interfaces/"
    ;;
  dotnet)
    local dep="$d/../interfaces-stub"
    mkdir -p "$dep"
    cp "$stubs/dotnet/stub/"*.cs "$dep/"
    cat >"$dep/Obs.csproj" <<'XML'
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
    <AssemblyName>Obs</AssemblyName>
    <Nullable>enable</Nullable>
  </PropertyGroup>
</Project>
XML
    sed 's|</Project>|  <ItemGroup><ProjectReference Include="../interfaces-stub/Obs.csproj" /></ItemGroup>\n</Project>|' "$d/Signals.csproj" >"$d/Signals.csproj.tmp"
    mv "$d/Signals.csproj.tmp" "$d/Signals.csproj"
    ;;
  dart)
    mkdir -p "$d/interfaces-stub"
    cp -r --no-preserve=mode "$stubs/dart/stub/." "$d/interfaces-stub/"
    cat >>"$d/pubspec.yaml" <<'YAML'
dependencies:
  diene_interfaces:
    path: interfaces-stub
YAML
    ;;
  esac
}

# Each scenario runs in a fresh process: poison/registration closure lasts until restart.
# shellcheck disable=SC2034 # consumed by the four compiler scripts
obs_port_cases=(merged repeat changed-hash changed-table duplicate-metric duplicate-event duplicate-span cross-kind late fresh-forgery)
obs_port_hash() { sed -n 's|^// contract-hash: ||p' "$1"; }
