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
        `'${where}' declares 'runtime: none', so skills-sync is deliberately off here — but this repository ` +
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
        `off here would ship no skills and say nothing. Name the runtime, or declare the opt-out explicitly with ` +
        `'runtime: none'. An absent configuration is an absence of declaration, not a declaration of absence.`,
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
  const spec = config.resolver!;

  info(`configuration: '${config.source}' (runtime: ${config.runtimeName})`);
  info(`repository root: '${repoRoot}'`);
  info(`vendor directory: '${vendorAbs}'`);

  const declared = declaredPackages(repoRoot, spec);
  info(
    `declared diene packages: ${declared.length}${declared.length ? ` — ${declared.map(d => d.name).join(', ')}` : ''}`,
  );

  const preconditionReasons = [...dependencyVerdict(repoRoot, spec, declared).reasons];

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
