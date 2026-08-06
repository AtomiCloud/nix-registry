import { existsSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { type Config, VENDOR_OWN_FILES } from './config.ts';
import {
  type DeclaredPackage,
  type Resolution,
  type TreeEntry,
  declaredPackages,
  dependencyVerdict,
  expectedTree,
  resolveInstalled,
  unresolvedReasons,
  walkFiles,
} from './engine.ts';
import { EXIT_OK, violation } from './exit.ts';
import { trackedUnder } from './git.ts';
import { info, skipped } from './report.ts';

export interface Plan {
  repoRoot: string;
  config: Config;
  vendorAbs: string;
  declared: DeclaredPackage[];
  resolution: Resolution;
  expected: TreeEntry[];
  // Empty when every precondition holds. Populated means: the ENVIRONMENT is
  // not ready, which is the one condition the three check tiers handle
  // differently. It never means the repository is wrong.
  preconditionReasons: string[];
}

// Files that are actually inside the vendor tree right now, excluding the two
// files the tool owns there.
export function vendorContent(vendorAbs: string): string[] {
  if (!existsSync(vendorAbs)) return [];
  try {
    if (!statSync(vendorAbs).isDirectory()) return [];
  } catch {
    return [];
  }
  return walkFiles(vendorAbs).filter(p => !VENDOR_OWN_FILES.includes(p));
}

// The state where skills-sync is OFF.
//
// Off is owner-ruled and legitimate: the central wiring is generic and INERT
// wherever no runtime is named. But "off" and "there are vendored skills here"
// cannot both be true. Left unchecked, deleting the configuration would be a
// way to switch the guarantee off while leaving the vendored tree in the
// repository — so that combination refuses instead of skipping.
export function outcomeWhenOff(config: Config, vendorAbs: string, repoRoot: string): number {
  const content = vendorContent(vendorAbs);
  const where = config.source ?? `${join(repoRoot, 'skills-sync.yaml')} (absent)`;

  if (content.length > 0) {
    throw violation(
      `skills-sync names no runtime in '${where}', but '${vendorAbs}' holds ${content.length} vendored file(s): ` +
        `${content.slice(0, 5).join(', ')}${content.length > 5 ? ', …' : ''}. ` +
        `A repository that vendors skills has a runtime; a repository that names none must have an empty vendor tree. ` +
        `Name the runtime, or remove the vendored tree.`,
    );
  }

  skipped(`skills-sync names no runtime in '${where}', so there is nothing to synchronise here.`);
  info(`vendor directory inspected: '${vendorAbs}' — 0 vendored file(s)`);
  return EXIT_OK;
}

export function buildPlan(repoRoot: string, config: Config): Plan {
  const vendorAbs = join(repoRoot, config.vendorDir);
  const spec = config.resolver!;

  info(`configuration: '${config.source}' (runtime: ${config.runtimeName})`);
  info(`repository root: '${repoRoot}'`);
  info(`vendor directory: '${vendorAbs}'`);

  const declared = declaredPackages(repoRoot, spec);
  info(
    `declared diene packages: ${declared.length}${declared.length ? ` — ${declared.map(d => d.name).join(', ')}` : ''}`,
  );

  const preconditionReasons = [...dependencyVerdict(repoRoot, spec, declared).reasons];

  // The resolution is only attempted when the cheap environment checks pass;
  // running `go list` with no go on PATH would turn a precondition into a tool
  // failure and lose the tier distinction entirely.
  let resolution: Resolution = { installed: [], unresolved: declared };
  if (preconditionReasons.length === 0) {
    resolution = resolveInstalled(repoRoot, spec, declared);
    preconditionReasons.push(...unresolvedReasons(resolution.unresolved));
  }

  const expected = preconditionReasons.length === 0 ? expectedTree(resolution.installed) : [];
  if (preconditionReasons.length === 0) {
    const shipping = resolution.installed.filter(p => p.skillsDir !== null);
    info(`installed packages resolved: ${resolution.installed.length}, of which ${shipping.length} ship skills`);
    info(`expected vendored files: ${expected.length}`);
  }

  return { repoRoot, config, vendorAbs, declared, resolution, expected, preconditionReasons };
}

// The tracked-subject guard.
//
// Comparing the expected tree against the WORKTREE alone would pass a
// repository whose vendor tree is correct on disk and absent from git — the
// files would be regenerated locally and shipped to nobody. So the committed
// state is a subject in its own right.
export function untrackedVendorFiles(repoRoot: string, vendorDir: string, vendorAbs: string): string[] {
  const tracked = new Set(
    trackedUnder(repoRoot, vendorDir)
      .map(p => p.slice(`${vendorDir}/`.length))
      .filter(p => p.length > 0),
  );
  return vendorContent(vendorAbs).filter(p => !tracked.has(p));
}
