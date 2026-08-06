import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { type Config, KEEP_NAME, MANIFEST_NAME } from './config.ts';
import { sha256 } from './engine.ts';
import { EXIT_OK, EXIT_PRECONDITION, EXIT_VIOLATION, toolFailure } from './exit.ts';
import { stagedSha256, trackedUnder } from './git.ts';
import { buildManifest, renderManifest } from './manifest.ts';
import { buildPlan, outcomeWhenOff, vendorContent } from './plan.ts';
import { info, ok, refusal, skipped, warn } from './report.ts';

function writeVendorTree(vendorAbs: string, config: Config, plan: ReturnType<typeof buildPlan>): string[] {
  const mutated: string[] = [];
  try {
    mkdirSync(vendorAbs, { recursive: true });
    if (!existsSync(join(vendorAbs, KEEP_NAME))) {
      writeFileSync(join(vendorAbs, KEEP_NAME), '');
      mutated.push(KEEP_NAME);
    }

    const expectedPaths = new Set(plan.expected.map(e => e.path));
    for (const entry of plan.expected) {
      const target = join(vendorAbs, entry.path);
      // Mutation detection is content-keyed so idempotent runs stay mutation-free.
      if (existsSync(target) && sha256(target) === entry.sha256) continue;
      mkdirSync(dirname(target), { recursive: true });
      // copyFileSync is required because cpSync may silently leave an existing destination unchanged.
      copyFileSync(entry.source, target);
      chmodSync(target, 0o644);
      mutated.push(entry.path);
    }

    for (const path of vendorContent(vendorAbs)) {
      if (expectedPaths.has(path)) continue;
      rmSync(join(vendorAbs, path), { force: true });
      mutated.push(path);
    }

    const manifestText = renderManifest(buildManifest(config.runtimeName as string, plan.declared, plan.expected));
    const manifestPath = join(vendorAbs, MANIFEST_NAME);
    if (!existsSync(manifestPath) || readFileSync(manifestPath, 'utf8') !== manifestText) {
      writeFileSync(manifestPath, manifestText);
      mutated.push(MANIFEST_NAME);
    }
  } catch (e) {
    throw toolFailure(`could not write the vendored tree at '${vendorAbs}': ${(e as Error).message}`);
  }
  return mutated.sort();
}

function indexDisagreements(
  repoRoot: string,
  config: Config,
  plan: ReturnType<typeof buildPlan>,
  vendorAbs: string,
): string[] {
  const expected = new Set([KEEP_NAME, MANIFEST_NAME, ...plan.expected.map(entry => entry.path)]);
  const indexed = trackedUnder(repoRoot, config.vendorDir)
    .map(path => path.slice(`${config.vendorDir}/`.length))
    .filter(path => path.length > 0);
  const candidates = [...new Set([...expected, ...indexed])].sort();

  return candidates
    .filter(path => {
      if (!expected.has(path)) return true;
      const tracked = `${config.vendorDir}/${path}`;
      return stagedSha256(repoRoot, tracked) !== sha256(join(vendorAbs, path));
    })
    .map(path => `${config.vendorDir}/${path}`);
}

export function runSync(repoRoot: string, config: Config, frozen: boolean): number {
  const vendorAbs = join(repoRoot, config.vendorDir);
  if (!config.enabled) return outcomeWhenOff(config, vendorAbs, repoRoot);

  const plan = buildPlan(repoRoot, config);
  info(`mode: ${frozen ? 'frozen' : 'writer'}`);

  if (plan.preconditionReasons.length > 0) {
    if (frozen) {
      skipped(`skills-sync sync --frozen: dependencies are not restored, so enforcement is skipped.`);
      for (const reason of plan.preconditionReasons) console.log(`   - ${reason}`);
      warn('Frozen enforcement did not run; restore dependencies before relying on vendored-state enforcement.');
      return EXIT_OK;
    }
    refusal(`skills-sync sync: dependencies for '${config.runtimeName}' are not restored in '${repoRoot}'.`);
    for (const reason of plan.preconditionReasons) console.error(`   - ${reason}`);
    console.error('   The writer never publishes a partial vendored tree. Restore dependencies, then run it again.');
    return EXIT_PRECONDITION;
  }

  if (plan.expected.length === 0 && config.requireSubjects) {
    refusal(
      `skills-sync sync${frozen ? ' --frozen' : ''}: no vendored skill resolved for runtime '${config.runtimeName}' in '${repoRoot}'. ` +
        `Writing an empty tree here would silently remove whatever is committed. ` +
        `If this repository legitimately vendors no skills, declare it: 'requireSubjects: false' in '${config.source}'.`,
    );
    return EXIT_VIOLATION;
  }

  const mutated = writeVendorTree(vendorAbs, config, plan);
  info(`writer mutated ${mutated.length} file(s)${mutated.length ? `: ${mutated.slice(0, 5).join(', ')}` : ''}`);

  if (frozen) {
    const indexStale = indexDisagreements(repoRoot, config, plan, vendorAbs);
    info(`index entries disagreeing with the expected tree: ${indexStale.length}`);

    if (mutated.length > 0 || indexStale.length > 0) {
      refusal(`skills-sync sync --frozen: the vendored tree changed or its git index disagrees.`);
      if (mutated.length > 0) {
        console.error(`   regenerated ${mutated.length} file(s) in your working tree:`);
        for (const path of mutated.slice(0, 10)) console.error(`     - ${config.vendorDir}/${path}`);
      }
      if (indexStale.length > 0) {
        console.error(`   ${indexStale.length} path(s) in the vendor index disagree:`);
        for (const path of indexStale.slice(0, 10)) console.error(`     - ${path}`);
      }
      console.error(`   NOTHING was added to your commit. Stage the vendored tree and commit again.`);
      return EXIT_VIOLATION;
    }
    ok(`vendored tree and git index match what the packages ship.`);
    return EXIT_OK;
  }

  ok(`vendored skills synchronised into '${vendorAbs}' (runtime '${config.runtimeName}'). Commit the result.`);
  return EXIT_OK;
}
