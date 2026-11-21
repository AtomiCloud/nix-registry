#!/usr/bin/env bash
# Generator fuzz: every contract in fixtures/fuzz/*.yaml uses tricky names (keywords of the four languages,
# core type names, names that collide after case conversion, bounds that do not fit Int32, ...). For each
# language `obs gen --lang L` must either REFUSE (exit non-zero) and write nothing, or write code that
# COMPILES in that language (warnings as errors, every analyzer on). A contract that fails `obs check`
# is a broken case, not a refusal.
#   fuzz.sh [--obs <obs binary>]       a language whose tool is not on PATH is skipped (named)
# Runs in the nix check (go, tsc, dotnet and dart are inputs of the obs-tests derivation).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091 # helper is relative to this fixture tree
source "$here/typecheck/stubs.bash"
OBS=obs
[ "${1:-}" = "--obs" ] && OBS="$2"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export HOME="$work/home" GOCACHE="$work/gocache" GOPATH="$work/gopath" GOFLAGS=-mod=mod GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0
export DOTNET_CLI_HOME="$work/home" DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1 MSBUILDDISABLENODEREUSE=1 DART_SUPPRESS_ANALYTICS=true
mkdir -p "$HOME"
fail=0

compile() { # compile <lang> <generated folder>; prints the compiler's complaint and returns 1 on failure
  local lang="$1" d="$2"
  case "$lang" in
  go)
    printf 'module example.com/fuzz\n\ngo 1.24\n' >"$d/go.mod"
    obs_setup_stub go "$d"
    (cd "$d" && go vet ./...) 2>&1
    ;;
  bun)
    obs_setup_stub bun "$d"
    (cd "$d" && tsc --noEmit --strict --noUncheckedIndexedAccess --exactOptionalPropertyTypes --target es2022 --module esnext --moduleResolution bundler --skipLibCheck signals.ts) 2>&1
    ;;
  dotnet)
    cat >"$d/Signals.csproj" <<'XML'
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
    obs_setup_stub dotnet "$d"
    local out
    out=$(cd "$d" && dotnet build Signals.csproj --disable-build-servers -p:UseSharedCompilation=false -nologo -v q 2>&1) || {
      grep ' error ' <<<"$out" | head -4
      return 1
    }
    grep -q 'Build succeeded' <<<"$out" || {
      echo "$out" | head -4
      return 1
    }
    ;;
  dart)
    printf 'name: fuzz_signals\npublish_to: none\nenvironment:\n  sdk: ^3.7.0\n' >"$d/pubspec.yaml"
    printf 'analyzer:\n  language:\n    strict-casts: true\n    strict-inference: true\n    strict-raw-types: true\n' >"$d/analysis_options.yaml"
    obs_setup_stub dart "$d"
    (cd "$d" && dart pub get --offline >/dev/null 2>&1) || true
    local out
    out=$(cd "$d" && dart analyze 2>&1) || {
      echo "$out" | head -4
      return 1
    }
    grep -q 'No issues found' <<<"$out" || {
      echo "$out" | head -4
      return 1
    }
    ;;
  esac
}

for f in "$here"/fixtures/fuzz/*.yaml; do
  name=$(basename "$f" .yaml)
  base="$work/$name"
  mkdir -p "$base/observability"
  cp "$f" "$base/observability/contract.yaml"
  if ! "$OBS" check --dir "$base/observability" >"$base/check.txt" 2>&1; then
    echo "FAIL $name: the case contract does not pass obs check: $(head -3 "$base/check.txt")"
    fail=1
    continue
  fi
  for lang in go dotnet dart bun; do
    tool=$lang
    [ "$lang" = bun ] && tool=tsc
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "SKIP $name/$lang: $tool not on PATH"
      continue
    fi
    r="$work/$name-$lang"
    rm -rf "$r"
    mkdir -p "$r/observability"
    cp "$f" "$r/observability/contract.yaml"
    cp "$here/fixtures/gen/good/"{go.mod,package.json,pubspec.yaml,Signals.csproj} "$r/"
    if timeout 60 "$OBS" gen --dir "$r/observability" --lang "$lang" >"$r/gen.txt" 2>&1; then
      if out=$(compile "$lang" "$r/gen/$lang"); then
        echo "PASS $name/$lang: generated code compiles"
      else
        echo "FAIL $name/$lang: generated code does not compile: $out"
        fail=1
      fi
    else
      rc=$?
      if [ "$rc" -eq 124 ]; then
        echo "FAIL $name/$lang: obs gen did not finish in 60 s"
        fail=1
      elif [ -e "$r/gen" ] || [ -e "$r/observability/contract" ]; then
        echo "FAIL $name/$lang: obs gen refused (exit $rc) but wrote files"
        fail=1
      else
        echo "PASS $name/$lang: refused before writing: $(head -1 "$r/gen.txt")"
      fi
    fi
  done
done
exit $fail
