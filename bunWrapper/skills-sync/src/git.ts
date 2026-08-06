import { toolFailure } from './exit.ts';

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

export function stagedSha256(root: string, path: string): string | null {
  const r = git(['show', `:${path}`], root);
  if (r.code !== 0) return null;
  const hasher = new Bun.CryptoHasher('sha256');
  hasher.update(r.raw);
  return hasher.digest('hex');
}
