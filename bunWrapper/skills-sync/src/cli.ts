import { CONFIG_DEFAULT, VENDOR_DEFAULT, loadConfig } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_USAGE, SkillsSyncError, usageError } from './exit.ts';
import { assertTierMatchesEnvironment, repoRoot } from './git.ts';
import { runCheck } from './check.ts';
import { runSync } from './sync.ts';
import { PRESETS, PRESET_NAMES } from './spec.ts';
import { TIERS, TIER_BEHAVIOUR, parseTier } from './tiers.ts';

// package.json is the single place the version is written; the nix derivation
// reads the same file, so the packaged name and `--version` cannot disagree.
import manifest from '../package.json' with { type: 'json' };

export const VERSION: string = (manifest as { version: string }).version;

// The subcommand table is the ONLY place a subcommand exists.
//
// It is written as a table, and dispatch is a lookup in it, because the usual
// alternative — a framework that serves root help before its unknown-command
// handler — lets a subcommand that was never implemented answer `--help` with
// the root usage and read as real. Here an unknown name cannot reach help at
// all: it is refused by name, and every subcommand prints ITS OWN usage.
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
  console.log(`skills-sync ${VERSION} — vendored-skill synchronisation and freshness

Usage:
  skills-sync <command> [options]
  skills-sync --help
  skills-sync --version

Commands:
${commandList()}

Run 'skills-sync <command> --help' for that command's own options.

Configuration:
  ./${CONFIG_DEFAULT} (override with $SKILLS_SYNC_CONFIG), read from the git work
  tree root. Naming no runtime is OFF: skills-sync is inert in a repository that
  vendors no skills, which is why one generic wiring can sit in every template.

    schemaVersion: 1
    runtime: bun            # ${PRESET_NAMES.join(' | ')}
    vendorDir: ${VENDOR_DEFAULT}
    requireSubjects: true

  A language skills-sync has never met is added with an inline 'resolver:' in
  this same file — one place, and no change to skills-sync, to workspace or to
  shared.

Exit codes:
  0  fresh, synchronised, or off
  1  the vendored tree is stale, or a guarantee-tier precondition failed
  2  usage error (including a declared tier that contradicts the environment)
  3  a precondition is unsatisfied: dependencies are not restored
  4  the configuration is invalid
  5  skills-sync could not complete the inspection`);
}

function optionValue(args: string[], flag: string): string | null {
  const exact = args.indexOf(flag);
  if (exact >= 0) {
    const value = args[exact + 1];
    if (value === undefined || value.startsWith('-')) throw usageError(`${flag} needs a value`);
    return value;
  }
  const inline = args.find(a => a.startsWith(`${flag}=`));
  return inline === undefined ? null : inline.slice(flag.length + 1);
}

function rejectUnknownOptions(command: string, args: string[], allowed: string[]): void {
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i] as string;
    if (!arg.startsWith('-')) throw usageError(`'skills-sync ${command}' takes no positional arguments, got '${arg}'`);
    const name = arg.includes('=') ? arg.slice(0, arg.indexOf('=')) : arg;
    if (!allowed.includes(name)) {
      throw usageError(`'skills-sync ${command}' has no option '${name}'; it accepts: ${allowed.join(', ')}`);
    }
    if (!arg.includes('=') && name !== '--help' && name !== '-h') i += 1;
  }
}

const checkCommand: Subcommand = {
  name: 'check',
  synopsis: 'skills-sync check --tier <setup|pre-commit|ci>',
  blurb: 'READ-ONLY. Refuse if the vendored tree is not what the packages ship.',
  usage: () => {
    console.log(`Usage: ${checkCommand.synopsis}

Compares the committed vendored tree against the skills the resolved packages
actually ship. Writes nothing, so it is the only half of skills-sync that a hook
may run.

Options:
  --tier <tier>   REQUIRED. One of: ${TIERS.join(', ')}.
  --help, -h      Print this message.

The tiers differ on exactly one thing — what an unrestored dependency tree
means. Nothing else is tiered:
${TIERS.map(t => `  ${TIER_BEHAVIOUR[t].summary}`).join('\n')}`);
  },
  run: args => {
    rejectUnknownOptions('check', args, ['--tier', '--help', '-h']);
    if (args.includes('--help') || args.includes('-h')) {
      checkCommand.usage();
      return EXIT_OK;
    }
    const raw = optionValue(args, '--tier');
    if (raw === null) {
      // Defaulting the tier would let a caller get the warning tier's silence
      // while believing it had the guarantee. The tier is always stated.
      throw usageError(
        `'skills-sync check' requires --tier; it is never defaulted. One of: ${TIERS.join(', ')}. See 'skills-sync check --help'.`,
      );
    }
    const tier = parseTier(raw);
    const root = repoRoot(process.cwd());
    return runCheck(root, loadConfig(root), tier);
  },
};

const syncCommand: Subcommand = {
  name: 'sync',
  synopsis: 'skills-sync sync [--tier <setup|pre-commit|ci>]',
  blurb: 'WRITER. Regenerate the vendored tree. Runs at setup, pre-commit and CI.',
  usage: () => {
    console.log(`Usage: ${syncCommand.synopsis}

Rewrites the vendored skills tree from the packages this repository declares and
has installed. This is the only thing that writes that tree (D11).

At pre-commit and in CI it NEVER STAGES ANYTHING. If it had to change the tree,
or if the INDEX does not already carry what the packages ship, it REFUSES and
leaves the regenerated files in your working tree for you to stage. A hook that
silently amends the commit is not acceptable, and CI must not repair-and-pass —
a defect repaired every cycle presents as no defect at all.

git commits the INDEX, which is why a mutation-only rule is not enough: a tree
regenerated by hand and left unstaged has nothing left to mutate, and the commit
would still record the stale tree.

Options:
  --tier <tier>   ${TIERS.join(' | ')}. Omit for a manual run.
  --help, -h      Print this message.`);
  },
  run: args => {
    rejectUnknownOptions('sync', args, ['--tier', '--help', '-h']);
    if (args.includes('--help') || args.includes('-h')) {
      syncCommand.usage();
      return EXIT_OK;
    }
    const raw = optionValue(args, '--tier');
    const tier = raw === null ? null : parseTier(raw);
    // The migrated D1, and the ordering is still load-bearing: it is evaluated
    // BEFORE any precondition, so it is reachable — and testable — in any
    // directory. It used to sit after `repoRoot`, which meant that outside a work
    // tree both the hook case and the ordinary case answered exit 5 and the rule
    // was never evaluated at all. Two arms agreeing reads exactly like a
    // controlled result, so a law another precondition can pre-empt is not
    // independently verifiable.
    assertTierMatchesEnvironment(tier);
    const root = repoRoot(process.cwd());
    return runSync(root, loadConfig(root), tier);
  },
};

const runtimesCommand: Subcommand = {
  name: 'runtimes',
  synopsis: 'skills-sync runtimes',
  blurb: 'List the built-in runtime presets a config may name.',
  usage: () => {
    console.log(`Usage: ${runtimesCommand.synopsis}

Prints every runtime name 'runtime:' accepts and the mechanism behind it. A
language that is not listed does not need a change here: name it with an inline
'resolver:' in the repository's own ${CONFIG_DEFAULT}.

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

const SUBCOMMANDS: Subcommand[] = [checkCommand, syncCommand, runtimesCommand];

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
    // Refused by name BEFORE any help handler can answer for it.
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
