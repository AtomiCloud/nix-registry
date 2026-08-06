import { toolFailure, usageError } from './exit.ts';

function git(args: string[], cwd: string): { code: number; out: string; err: string; raw: Uint8Array } {
  const run = Bun.spawnSync(['git', ...args], { cwd, stdout: 'pipe', stderr: 'pipe' });
  const decoder = new TextDecoder();
  return {
    code: run.exitCode ?? 1,
    out: decoder.decode(run.stdout),
    err: decoder.decode(run.stderr).trim(),
    raw: run.stdout,
  };
}

// Every path this tool touches is resolved against the work tree root, never
// against an inherited working directory. A relative path read from an
// inherited cwd is how a correctly-operated instrument ends up aimed at the
// wrong repository.
export function repoRoot(from: string): string {
  if (Bun.which('git') === null) {
    throw toolFailure('git is not on PATH; skills-sync needs it to tell tracked content from untracked');
  }
  const r = git(['rev-parse', '--show-toplevel'], from);
  if (r.code !== 0) {
    throw toolFailure(`'${from}' is not inside a git work tree (git rev-parse --show-toplevel: ${r.err})`);
  }
  const root = r.out.trim();
  if (root.length === 0) throw toolFailure(`could not determine the work tree root from '${from}'`);
  return root;
}

export function trackedUnder(root: string, path: string): string[] {
  const r = git(['ls-files', '-z', '--', path], root);
  if (r.code !== 0) throw toolFailure(`could not list tracked files under '${path}' (git ls-files: ${r.err})`);
  return r.out.split('\0').filter(p => p.length > 0);
}

// --------------------------------------------------------------------------- //
// D1 — MIGRATED, NOT DELETED
// --------------------------------------------------------------------------- //

// D1's never-in-hooks clause was REVOKED by the owner, who mandated that the
// writer run at setup, pre-commit AND CI. So the REFUSAL is gone.
//
// The DETECTION is not. These markers remain the best evidence available of
// WHERE the tool is running, and the tiers now behave differently, so a declared
// tier that contradicts the environment is a wiring mistake. That is the one
// thing the old rule caught which is still worth catching: a setup script wired
// into a hook.
//
// Each marker is a variable a hook runner sets and an interactive shell does
// not. The variable that fired is named in the refusal, because "this looks like
// a hook" is not something a user can act on.
const HOOK_MARKERS = [
  'SKILLS_SYNC_HOOK_CONTEXT', // an explicit declaration by any wrapper
  'PRE_COMMIT', // pre-commit framework
  'PRE_COMMIT_HOME',
  'GIT_INDEX_FILE', // git sets this for the hooks that run during a commit
  'HUSKY_GIT_PARAMS',
  'LEFTHOOK',
];

export function hookMarker(): string | null {
  for (const name of HOOK_MARKERS) {
    const value = process.env[name];
    if (value !== undefined && value !== '' && value !== '0') return name;
  }
  return null;
}

export const HOOK_MARKER_NAMES = HOOK_MARKERS;

// The migrated form: the markers no longer refuse, they check that the DECLARED
// tier is consistent with the environment the tool actually finds itself in.
export function assertTierMatchesEnvironment(tier: string | null): void {
  const marker = hookMarker();
  if (marker === null) return;

  if (tier === null) {
    throw usageError(
      `'skills-sync sync' is running in a hook context — the environment variable ${marker} says so — ` +
        `but no --tier was declared. The tier decides what the writer does on drift, so it is never ` +
        `inferred. Pass --tier pre-commit (or --tier ci). Hook markers checked: ${HOOK_MARKERS.join(', ')}.`,
    );
  }
  if (tier === 'setup') {
    throw usageError(
      `'skills-sync sync --tier setup' is running in a hook context — ${marker} says so. Setup restores ` +
        `dependencies and then synchronises strictly; a hook is not setup. This is a wiring mistake: use ` +
        `--tier pre-commit or --tier ci. Hook markers checked: ${HOOK_MARKERS.join(', ')}.`,
    );
  }
}

// The sha256 of a path AS STAGED IN THE INDEX, or null when it is not staged.
//
// git commits the INDEX. A rule that compares only the worktree tests the wrong
// artifact: a user who regenerates by hand and does not stage leaves nothing to
// mutate, and a mutation-only check passes while the commit ships the stale tree.
export function stagedSha256(root: string, path: string): string | null {
  const r = git(['show', `:${path}`], root);
  if (r.code !== 0) return null;
  const hasher = new Bun.CryptoHasher('sha256');
  hasher.update(r.raw);
  return hasher.digest('hex');
}
