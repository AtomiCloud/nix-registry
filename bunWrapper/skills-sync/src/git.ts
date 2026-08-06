import { toolFailure, usageError } from './exit.ts';

function git(args: string[], cwd: string): { code: number; out: string; err: string } {
  const run = Bun.spawnSync(['git', ...args], { cwd, stdout: 'pipe', stderr: 'pipe' });
  const decoder = new TextDecoder();
  return { code: run.exitCode ?? 1, out: decoder.decode(run.stdout), err: decoder.decode(run.stderr).trim() };
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
// D1 — SYNC-WRITER-NEVER-HOOKS
// --------------------------------------------------------------------------- //

// The rule that no hook may write the vendor tree is enforced BY THE TOOL, not
// only by where the tool is wired. A downstream repository that adds
// `skills-sync sync` to a hook gets a refusal at the first commit rather than a
// silent violation that survives until someone reads the hook file.
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

export function refuseInHookContext(subcommand: string): void {
  const marker = hookMarker();
  if (marker === null) return;
  throw usageError(
    `'skills-sync ${subcommand}' writes the vendor tree and must never run from a hook (D1). ` +
      `The environment variable ${marker} says this is a hook context. ` +
      `Run the writer at setup or by hand; a hook runs 'skills-sync check --tier pre-commit', which is read-only. ` +
      `Hook markers checked: ${HOOK_MARKERS.join(', ')}.`,
  );
}
