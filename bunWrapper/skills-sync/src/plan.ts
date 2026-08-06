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
import { PRESETS, type ResolverSpec } from './spec.ts';
import { EXIT_OK, violation } from './exit.ts';
import { info, skipped, warn } from './report.ts';

export interface Plan {
  repoRoot: string;
  config: Config;
  vendorAbs: string;
  declared: DeclaredPackage[];
  resolution: Resolution;
  expected: TreeEntry[];
  preconditionReasons: string[];
}

export function vendorContent(vendorAbs: string): string[] {
  if (!existsSync(vendorAbs)) return [];
  try {
    if (!statSync(vendorAbs).isDirectory()) return [];
  } catch {
    return [];
  }
  return walkFiles(vendorAbs).filter(p => !VENDOR_OWN_FILES.includes(p));
}

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

  const declared = probeForUnclaimedSubject(repoRoot);

  if (declared.packages.length > 0) {
    const named = declared.packages.map(p => `${p.name} (from ${p.declaredIn})`);
    if (config.explicitOptOut) {
      warn(
        `'${where}' declares 'runtimes: []', so skills-sync is deliberately off here — but this repository ` +
          `declares ${declared.packages.length} diene package(s): ${named.join(', ')}. ` +
          `Their skills are NOT vendored. That is accepted because the opt-out is explicit.`,
      );
      info(
        `off-probe: ${declared.mechanisms} mechanism(s) probed (${declared.names}), ${declared.packages.length} diene package(s) declared`,
      );
      return EXIT_OK;
    }
    throw violation(
      `'${where}' names no runtime, but this repository declares ${declared.packages.length} diene package(s): ` +
        `${named.join(', ')}. A repository that declares diene packages has a vendored-skills subject, so being ` +
        `off here would ship no skills and say nothing. Declare the runtimes, or opt out explicitly with ` +
        `'runtimes: []'. An absent configuration is an absence of declaration, not a declaration of absence.`,
    );
  }

  skipped(`skills-sync names no runtime in '${where}', so there is nothing to synchronise here.`);
  info(`vendor directory inspected: '${vendorAbs}' — 0 vendored file(s)`);
  info(`off-probe: ${declared.mechanisms} mechanism(s) probed (${declared.names}), 0 diene package(s) declared`);
  return EXIT_OK;
}

function probeForUnclaimedSubject(repoRoot: string): {
  packages: DeclaredPackage[];
  mechanisms: number;
  names: string;
} {
  const seen = new Map<string, ResolverSpec>();
  for (const preset of Object.values(PRESETS)) seen.set(preset.name, preset);

  const packages: DeclaredPackage[] = [];
  for (const spec of seen.values()) {
    for (const pkg of declaredPackages(repoRoot, spec, true)) {
      if (!packages.some(p => p.name === pkg.name)) packages.push(pkg);
    }
  }
  return { packages, mechanisms: seen.size, names: [...seen.keys()].sort().join(', ') };
}

export function buildPlan(repoRoot: string, config: Config): Plan {
  const vendorAbs = join(repoRoot, config.vendorDir);

  info(`configuration: '${config.source}' (runtimes: ${config.runtimes.join(', ')})`);
  info(`repository root: '${repoRoot}'`);
  info(`vendor directory: '${vendorAbs}'`);

  const declared: DeclaredPackage[] = [];
  const preconditionReasons: string[] = [];
  const installed: Resolution['installed'] = [];
  const unresolved: DeclaredPackage[] = [];

  for (const spec of config.resolvers) {
    const specDeclared = declaredPackages(repoRoot, spec).filter(d => !declared.some(p => p.name === d.name));
    declared.push(...specDeclared);
    const reasons = [...dependencyVerdict(repoRoot, spec, specDeclared).reasons];
    if (reasons.length > 0) {
      preconditionReasons.push(...reasons);
      unresolved.push(...specDeclared);
      continue;
    }
    const resolution = resolveInstalled(repoRoot, spec, specDeclared);
    preconditionReasons.push(...unresolvedReasons(resolution.unresolved));
    unresolved.push(...resolution.unresolved);
    installed.push(...resolution.installed);
  }
  info(
    `declared diene packages: ${declared.length}${declared.length ? ` — ${declared.map(d => d.name).join(', ')}` : ''}`,
  );

  let expected = preconditionReasons.length === 0 ? expectedTree(installed) : [];
  if (preconditionReasons.length === 0) {
    // Two runtimes shipping the same vendored path with different content is a
    // real conflict a human must resolve, never a silent last-writer-wins.
    const byPath = new Map<string, string>();
    for (const entry of expected) {
      const prior = byPath.get(entry.path);
      if (prior !== undefined && prior !== entry.sha256) {
        preconditionReasons.push(
          `vendored path '${entry.path}' is shipped with different content by two declared runtimes; resolve the collision in the packages`,
        );
      }
      byPath.set(entry.path, entry.sha256);
    }
    if (preconditionReasons.length > 0) expected = [];
  }
  if (preconditionReasons.length === 0) {
    const shipping = installed.filter(p => p.skillsDir !== null);
    info(`installed packages resolved: ${installed.length}, of which ${shipping.length} ship skills`);
    info(`expected vendored files: ${expected.length}`);
  }

  const resolution: Resolution = { installed, unresolved };
  return { repoRoot, config, vendorAbs, declared, resolution, expected, preconditionReasons };
}
