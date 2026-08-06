import { readFileSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { DlintError, usage } from './exit.ts';

interface LockNode {
  original?: { ref?: unknown; rev?: unknown };
  locked?: { rev?: unknown };
}

interface LockFile {
  nodes?: Record<string, LockNode | { inputs?: unknown }>;
}

export interface CheckResult {
  code: number;
  output: string[];
}

function pathFrom(root: string, value: string): string {
  return isAbsolute(value) ? value : join(root, value);
}

function option(config: unknown, name: string, fallback: string): string {
  if (config === null || typeof config !== 'object' || Array.isArray(config)) usage('nixpkgs-pin must be a mapping');
  const value = (config as Record<string, unknown>)[name];
  if (value === undefined) return fallback;
  if (typeof value !== 'string' || value.length === 0) usage(`nixpkgs-pin.${name} must be a non-empty string`);
  return value;
}

function readLock(path: string): LockFile {
  try {
    const parsed = JSON.parse(readFileSync(path, 'utf8'));
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed))
      usage(`'${path}' must be a JSON object`);
    return parsed as LockFile;
  } catch (error) {
    if (error instanceof DlintError) throw error;
    usage(`could not parse '${path}': ${(error as Error).message}`);
  }
}

function declaredRevisions(flake: string, pattern: RegExp): string[] {
  let text = '';
  try {
    text = readFileSync(flake, 'utf8');
  } catch (error) {
    usage(`could not read '${flake}': ${(error as Error).message}`);
  }
  const result: string[] = [];
  for (const line of text.split('\n')) {
    const name = line.match(/([A-Za-z0-9_-]+)\.url\s*=/)?.[1];
    if (!name || !pattern.test(name)) continue;
    pattern.lastIndex = 0;
    result.push(...(line.match(/[0-9a-f]{40}/g) ?? []));
  }
  return result;
}

export function checkNixpkgsPin(root: string, config: unknown): CheckResult {
  const flake = pathFrom(root, option(config, 'flake', 'flake.nix'));
  const lockPath = pathFrom(root, option(config, 'lock', 'flake.lock'));
  const inputPattern = option(config, 'inputPattern', 'nixpkgs');
  let pattern: RegExp;
  try {
    pattern = new RegExp(inputPattern);
  } catch (error) {
    usage(`nixpkgs-pin.inputPattern is not a regular expression: ${(error as Error).message}`);
  }
  const lock = readLock(lockPath);
  const rootInputs = (lock.nodes?.root as { inputs?: unknown } | undefined)?.inputs;
  if (rootInputs === null || typeof rootInputs !== 'object' || Array.isArray(rootInputs)) {
    usage(`'${lockPath}' has no nodes.root.inputs mapping`);
  }
  const selected = Object.entries(rootInputs as Record<string, unknown>).filter(([name]) => pattern.test(name));
  pattern.lastIndex = 0;
  if (selected.length === 0) return { code: 1, output: [`no root input matches '${inputPattern}'`] };

  const failures: string[] = [];
  const lockedRevisions: string[] = [];
  for (const [, nodeName] of selected) {
    if (typeof nodeName !== 'string') {
      failures.push('a root input does not resolve to a lock node');
      continue;
    }
    const node = lock.nodes?.[nodeName] as LockNode | undefined;
    const originalRev = node?.original?.rev;
    const lockedRev = node?.locked?.rev;
    const ref = node?.original?.ref;
    if (typeof lockedRev === 'string') lockedRevisions.push(lockedRev);
    if (typeof ref === 'string' && ref.length > 0) {
      failures.push(`root input '${nodeName}' follows channel '${ref}'`);
      continue;
    }
    if (typeof originalRev !== 'string' || !/^[0-9a-f]{40}$/.test(originalRev)) {
      failures.push(
        `root input '${nodeName}' declares '${String(originalRev ?? '')}', not an exact 40-character commit`,
      );
      continue;
    }
    if (lockedRev !== originalRev) {
      failures.push(`root input '${nodeName}' asks for ${originalRev} but is locked to ${String(lockedRev ?? '')}`);
      continue;
    }
  }
  for (const revision of declaredRevisions(flake, pattern)) {
    if (!lockedRevisions.includes(revision))
      failures.push(`'${flake}' declares ${revision} but no root input is locked to it`);
  }
  if (failures.length > 0) return { code: 1, output: failures };
  return {
    code: 0,
    output: [`root inputs inspected: ${selected.length}`, 'every declared input is pinned to an exact commit'],
  };
}
