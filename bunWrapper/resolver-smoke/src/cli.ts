import { loadConfig } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_USAGE, ResolverSmokeError, usageError } from './exit.ts';
import { OWN_DISCLOSURE_CAP, PUBLISHED_DISCLOSURE_CAP } from './loss.ts';
import { runSmoke } from './run.ts';
import { loadPublishedResolver } from './vendor.ts';
import { VERSION } from './version.ts';

export { VERSION };

function usage(): void {
  console.log(`resolver-smoke ${VERSION} — published-resolver compatibility gate

Usage:
  resolver-smoke [--config <path>] [--full] [--json]
  resolver-smoke --help
  resolver-smoke --version

Run from the repository root. Every present atomi/nix@2 dispatch file is fed to
the real, hash-pinned published resolver with a structure-aware synthetic child.

Options:
  --config <path>  read configuration from an explicit path
  --full   print every lost material unit, plus this arm's synthetic-child
           contribution and the candidate remainder of the published
           resolver's withheld names
  --json   emit one complete machine-readable report on stdout and suppress
           the human lines (implies --full detail)

By default the lost-material list is TRUNCATED: resolver-smoke shows its first
${OWN_DISCLOSURE_CAP} units, and the published resolver's own refusal message shows its first ${PUBLISHED_DISCLOSURE_CAP}.
Counts are DISTINCT (kind, name) units deduplicated across the arm's probe
inputs — never occurrences.

Configuration (optional):
  resolver-smoke.yaml (override with $RESOLVER_SMOKE_CONFIG or --config)

    schemaVersion: 1
    requireSubjects: true

Exit codes:
  0  every present resolver-managed file passed, or absence was declared
  1  resolver compatibility violation or an undeclared vacuous run
  2  usage error
  4  invalid configuration or vendored-resolver integrity failure
  5  resolver-smoke could not complete the inspection`);
}

interface Invocation {
  configPath?: string;
  full: boolean;
  json: boolean;
}

const CONFIG_PREFIX = '--config=';

/**
 * A real parser, because the flags now combine.
 *
 * The previous version matched the exact shape of argv — two elements meaning
 * `--config <path>`, one meaning `--config=<path>` — which cannot express
 * `--full --config x.yaml` and could not be extended to. What it got right, and
 * what is preserved here, is that anything unrecognised is a usage error naming
 * the whole invocation: arms A3 and A5 depend on an unknown flag or a stray
 * positional exiting 2 rather than falling through to a run, and an operator who
 * mistyped a flag needs to see what resolver-smoke actually received.
 */
function parseInvocation(argv: string[]): Invocation {
  const parsed: Invocation = { full: false, json: false };
  const refuse = (why: string): never => {
    throw usageError(`${why} in '${argv.join(' ')}'; resolver-smoke accepts only --config <path>, --full and --json`);
  };

  for (let index = 0; index < argv.length; index++) {
    const argument = argv[index];
    if (argument === '--full') {
      parsed.full = true;
      continue;
    }
    if (argument === '--json') {
      parsed.json = true;
      continue;
    }
    if (argument === '--config') {
      // Consumed here rather than left for a second pass so that a `--config`
      // with nothing after it is a usage error instead of silently reading the
      // default search path.
      const value = argv[index + 1];
      if (value === undefined || value.length === 0) refuse('--config names no path');
      if (parsed.configPath !== undefined) refuse('--config is given twice');
      parsed.configPath = value;
      index++;
      continue;
    }
    if (argument.startsWith(CONFIG_PREFIX)) {
      const value = argument.slice(CONFIG_PREFIX.length);
      if (value.length === 0) refuse('--config names no path');
      if (parsed.configPath !== undefined) refuse('--config is given twice');
      parsed.configPath = value;
      continue;
    }
    refuse(`unknown argument '${argument}'`);
  }
  return parsed;
}

export async function main(argv: string[]): Promise<number> {
  if (argv.length === 1 && (argv[0] === '--help' || argv[0] === '-h')) {
    usage();
    return EXIT_OK;
  }
  if (argv.length === 1 && (argv[0] === '--version' || argv[0] === '-V')) {
    try {
      await loadPublishedResolver();
      console.log(`resolver-smoke ${VERSION}`);
      return EXIT_OK;
    } catch (error) {
      if (error instanceof ResolverSmokeError) {
        console.error(`❌ ${error.message}`);
        return error.code;
      }
      console.error(`❌ resolver-smoke failed unexpectedly: ${(error as Error).stack ?? String(error)}`);
      return EXIT_TOOL;
    }
  }

  let invocation: Invocation;
  try {
    invocation = parseInvocation(argv);
  } catch (error) {
    console.error(`❌ ${(error as Error).message}`);
    console.error("Run 'resolver-smoke --help' for usage.");
    return error instanceof ResolverSmokeError ? error.code : EXIT_USAGE;
  }

  try {
    const vendor = await loadPublishedResolver();
    const config = loadConfig(process.cwd(), invocation.configPath);
    return await runSmoke(process.cwd(), config, vendor.resolver, {
      // `--json` implies full detail: a machine-readable report that truncated
      // its own lists would be the defect this mode exists to remove.
      full: invocation.full || invocation.json,
      json: invocation.json,
      vendor: { path: vendor.path, sha256: vendor.sha256 },
    });
  } catch (error) {
    if (error instanceof ResolverSmokeError) {
      console.error(`❌ ${error.message}`);
      return error.code;
    }
    console.error(`❌ resolver-smoke failed unexpectedly: ${(error as Error).stack ?? String(error)}`);
    return EXIT_TOOL;
  }
}
