import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { type Config, MANIFEST_NAME } from './config.ts';
import { sha256 } from './engine.ts';
import { EXIT_OK, EXIT_VIOLATION } from './exit.ts';
import { buildPlan, outcomeWhenOff, untrackedVendorFiles, vendorContent } from './plan.ts';
import { buildManifest, describeManifestDrift } from './manifest.ts';
import { info, ok, refusal } from './report.ts';
import { type Tier, applyPrecondition } from './tiers.ts';

// `skills-sync check` is READ-ONLY by construction: nothing below writes to the
// repository, and the expected tree is computed by hashing the resolved sources
// in place rather than by regenerating into the work tree. That is what lets the
// same subcommand run in a hook (D1) — the WRITER never does.
export function runCheck(repoRoot: string, config: Config, tier: Tier): number {
  const vendorAbs = join(repoRoot, config.vendorDir);

  if (!config.enabled) {
    info(`tier: ${tier}`);
    return outcomeWhenOff(config, vendorAbs, repoRoot);
  }

  info(`tier: ${tier}`);
  const plan = buildPlan(repoRoot, config);

  if (plan.preconditionReasons.length > 0) {
    return applyPrecondition(tier, `'${config.runtimeName}' in '${repoRoot}'`, plan.preconditionReasons);
  }

  const problems: string[] = [];

  // A check with no subject passes for a reason that has nothing to do with the
  // repository being correct. Where a repository legitimately vendors nothing,
  // saying so is a declaration, never a silent default.
  if (plan.expected.length === 0 && config.requireSubjects) {
    refusal(
      `no vendored skill resolved for runtime '${config.runtimeName}' in '${repoRoot}', so this check would pass vacuously. ` +
        `If this repository legitimately vendors no skills, declare it: 'requireSubjects: false' in '${config.source}'.`,
    );
    return EXIT_VIOLATION;
  }

  // 1. content: what is on disk versus what the resolved packages ship.
  const expectedByPath = new Map(plan.expected.map(e => [e.path, e.sha256]));
  const actualPaths = vendorContent(vendorAbs);
  const actualByPath = new Map(actualPaths.map(p => [p, sha256(join(vendorAbs, p))]));

  const missing = [...expectedByPath.keys()].filter(p => !actualByPath.has(p)).sort();
  const extra = [...actualByPath.keys()].filter(p => !expectedByPath.has(p)).sort();
  const changed = [...expectedByPath.keys()]
    .filter(p => actualByPath.has(p) && actualByPath.get(p) !== expectedByPath.get(p))
    .sort();

  info(`vendored files on disk: ${actualPaths.length}`);
  for (const p of missing) problems.push(`missing from the vendored tree: ${config.vendorDir}/${p}`);
  for (const p of extra)
    problems.push(`present in the vendored tree but shipped by no resolved package: ${config.vendorDir}/${p}`);
  for (const p of changed) problems.push(`content differs from the package that ships it: ${config.vendorDir}/${p}`);

  // 2. committed state: a tree that is right on disk and untracked ships to
  // nobody, so it is not fresh.
  const untracked = untrackedVendorFiles(repoRoot, config.vendorDir, vendorAbs);
  for (const p of untracked) problems.push(`vendored but not tracked by git: ${config.vendorDir}/${p}`);
  info(`vendored files untracked by git: ${untracked.length}`);

  // 3. the manifest describes the tree it sits in.
  //
  // An empty tree may carry no manifest: a repository that has declared it
  // vendors nothing (requireSubjects: false) has nothing for a manifest to
  // describe, and demanding one there would be friction with no subject behind
  // it. A manifest that IS present is always checked, empty tree or not.
  const manifestPath = join(vendorAbs, MANIFEST_NAME);
  const manifestText = existsSync(manifestPath) ? readFileSync(manifestPath, 'utf8') : null;
  const manifestOptional = plan.expected.length === 0 && actualPaths.length === 0 && manifestText === null;
  if (!manifestOptional) {
    const manifestDrift = describeManifestDrift(
      buildManifest(config.runtimeName as string, plan.declared, plan.expected),
      manifestText,
    );
    if (manifestDrift !== null) problems.push(`${manifestDrift} (${config.vendorDir}/${MANIFEST_NAME})`);
  }

  if (problems.length > 0) {
    refusal(
      `the vendored skills in '${vendorAbs}' are stale (${problems.length} finding(s)). Run 'skills-sync sync' and commit the result:`,
    );
    for (const p of problems) console.error(`   - ${p}`);
    return EXIT_VIOLATION;
  }

  ok(
    `vendored skills in '${vendorAbs}' are fresh: ${plan.expected.length} file(s) from ` +
      `${plan.resolution.installed.filter(p => p.skillsDir !== null).length} package(s), runtime '${config.runtimeName}'`,
  );
  return EXIT_OK;
}
