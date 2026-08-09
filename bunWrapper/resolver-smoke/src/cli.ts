import manifest from '../package.json' with { type: 'json' };
import { loadConfig } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_USAGE, ResolverSmokeError, usageError } from './exit.ts';
import { runSmoke } from './run.ts';
import { loadPublishedResolver } from './vendor.ts';

export const VERSION: string = (manifest as { version: string }).version;

function usage(): void {
  console.log(`resolver-smoke ${VERSION} — published-resolver compatibility gate

Usage:
  resolver-smoke [--config <path>]
  resolver-smoke --help
  resolver-smoke --version

Run from the repository root. Every present atomi/nix@2 dispatch file is fed to
the real, hash-pinned published resolver with a structure-aware synthetic child.

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

function parseConfigArgument(argv: string[]): string | undefined {
  if (argv.length === 0) return undefined;
  if (argv.length === 2 && argv[0] === '--config' && argv[1].length > 0) return argv[1];
  if (argv.length === 1 && argv[0].startsWith('--config=') && argv[0].length > '--config='.length) {
    return argv[0].slice('--config='.length);
  }
  throw usageError(`unknown invocation '${argv.join(' ')}'; resolver-smoke accepts only --config <path>`);
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

  let configPath: string | undefined;
  try {
    configPath = parseConfigArgument(argv);
  } catch (error) {
    console.error(`❌ ${(error as Error).message}`);
    console.error("Run 'resolver-smoke --help' for usage.");
    return error instanceof ResolverSmokeError ? error.code : EXIT_USAGE;
  }

  try {
    const resolver = await loadPublishedResolver();
    const config = loadConfig(process.cwd(), configPath);
    return await runSmoke(process.cwd(), config, resolver);
  } catch (error) {
    if (error instanceof ResolverSmokeError) {
      console.error(`❌ ${error.message}`);
      return error.code;
    }
    console.error(`❌ resolver-smoke failed unexpectedly: ${(error as Error).stack ?? String(error)}`);
    return EXIT_TOOL;
  }
}
