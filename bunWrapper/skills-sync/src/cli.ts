import { CONFIG_DEFAULT, VENDOR_DEFAULT, loadConfig } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_USAGE, SkillsSyncError, usageError } from './exit.ts';
import { repoRoot } from './git.ts';
import { runSync } from './sync.ts';
import { PRESETS, PRESET_NAMES } from './spec.ts';
import manifest from '../package.json' with { type: 'json' };

export const VERSION: string = (manifest as { version: string }).version;

interface Subcommand {
  name: string;
  synopsis: string;
  blurb: string;
  usage: () => void;
  run: (args: string[]) => number;
}

function commandList(): string {
  return SUBCOMMANDS.map(c => `  ${c.name.padEnd(10)}${c.blurb}`).join('\n');
}

function rootUsage(): void {
  console.log(`skills-sync ${VERSION} — vendored-skill synchronisation

Usage:
  skills-sync <command> [options]
  skills-sync --help
  skills-sync --version

Commands:
${commandList()}

Run 'skills-sync <command> --help' for command options.

Configuration:
  <git-root>/${CONFIG_DEFAULT} (override with $SKILLS_SYNC_CONFIG)

    schemaVersion: 1
    runtime: bun            # ${PRESET_NAMES.join(' | ')}
    vendorDir: ${VENDOR_DEFAULT}
    requireSubjects: true

Exit codes:
  0  synchronised, frozen-clean, skipped, or off
  1  the configured vendored-state contract is violated
  2  usage error
  3  bare sync cannot run because dependencies are not restored
  4  invalid configuration
  5  inspection or writing failed`);
}

function rejectUnknownOptions(command: string, args: string[], allowed: string[]): void {
  for (const arg of args) {
    if (!arg.startsWith('-')) {
      throw usageError(`'skills-sync ${command}' takes no positional arguments, got '${arg}'`);
    }
    if (!allowed.includes(arg)) {
      throw usageError(`'skills-sync ${command}' has no option '${arg}'; it accepts: ${allowed.join(', ')}`);
    }
  }
}

const syncCommand: Subcommand = {
  name: 'sync',
  synopsis: 'skills-sync sync [--frozen]',
  blurb: 'Write the vendor tree; optionally enforce worktree and index freshness.',
  usage: () => {
    console.log(`Usage: ${syncCommand.synopsis}

Without --frozen, writes the complete vendor tree and refuses with exit 3 when
dependencies are not restored.

With --frozen, unrestored dependencies are reported and skipped with exit 0.
Otherwise it writes, then refuses with exit 1 if anything changed or the git
index disagrees with the complete vendor tree. It never stages files.

Options:
  --frozen        Enforce mutation-free and index-matching vendored state.
  --help, -h      Print this message.`);
  },
  run: args => {
    rejectUnknownOptions('sync', args, ['--frozen', '--help', '-h']);
    if (args.includes('--help') || args.includes('-h')) {
      syncCommand.usage();
      return EXIT_OK;
    }
    const root = repoRoot(process.cwd());
    return runSync(root, loadConfig(root), args.includes('--frozen'));
  },
};

const runtimesCommand: Subcommand = {
  name: 'runtimes',
  synopsis: 'skills-sync runtimes',
  blurb: 'List built-in runtime presets.',
  usage: () => {
    console.log(`Usage: ${runtimesCommand.synopsis}

Lists the built-in runtime presets accepted by ${CONFIG_DEFAULT}. Unknown
runtimes can use an inline resolver in that file.

Options:
  --help, -h      Print this message.`);
  },
  run: args => {
    rejectUnknownOptions('runtimes', args, ['--help', '-h']);
    if (args.includes('--help') || args.includes('-h')) {
      runtimesCommand.usage();
      return EXIT_OK;
    }
    for (const name of PRESET_NAMES) {
      const preset = PRESETS[name]!;
      const sources = preset.declare.map(d => d.file ?? d.glob).join(', ');
      console.log(
        `${name.padEnd(10)} mechanism=${preset.name.padEnd(6)} strategy=${preset.resolve.strategy.padEnd(13)} declares from: ${sources}`,
      );
    }
    console.log(
      `\n${PRESET_NAMES.length} preset name(s) over ${new Set(Object.values(PRESETS).map(p => p.name)).size} mechanism(s).`,
    );
    return EXIT_OK;
  },
};

const SUBCOMMANDS: Subcommand[] = [syncCommand, runtimesCommand];

export function main(argv: string[]): number {
  const [command, ...rest] = argv;

  if (command === undefined || command === '') {
    console.error(`❌ skills-sync needs a command.`);
    console.error(`Commands:\n${commandList()}`);
    return EXIT_USAGE;
  }
  if (command === '--help' || command === '-h' || command === 'help') {
    rootUsage();
    return EXIT_OK;
  }
  if (command === '--version' || command === '-V') {
    console.log(`skills-sync ${VERSION}`);
    return EXIT_OK;
  }

  const found = SUBCOMMANDS.find(c => c.name === command);
  if (!found) {
    console.error(
      `❌ unknown command '${command}'; skills-sync has exactly ${SUBCOMMANDS.length}: ${SUBCOMMANDS.map(c => c.name).join(', ')}`,
    );
    return EXIT_USAGE;
  }

  try {
    return found.run(rest);
  } catch (e) {
    if (e instanceof SkillsSyncError) {
      console.error(`❌ ${e.message}`);
      return e.code;
    }
    console.error(`❌ skills-sync ${command} failed unexpectedly: ${(e as Error).stack ?? String(e)}`);
    return EXIT_TOOL;
  }
}
