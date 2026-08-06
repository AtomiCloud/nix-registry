import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { type Config, KEEP_NAME, MANIFEST_NAME } from './config.ts';
import { EXIT_OK, EXIT_PRECONDITION, EXIT_VIOLATION, toolFailure } from './exit.ts';
import { refuseInHookContext } from './git.ts';
import { buildManifest, renderManifest } from './manifest.ts';
import { buildPlan, outcomeWhenOff, vendorContent } from './plan.ts';
import { info, ok, refusal } from './report.ts';

// `skills-sync sync` is the WRITER. It runs at setup and by hand — never in a
// hook (D1), which is enforced below rather than merely documented.
//
// It has no degraded mode. The old shell resolver kept a "dependencies are not
// restored, keep the committed tree, exit 0" branch because the same code also
// ran at pre-commit. Under this contract the writer does not run at pre-commit
// at all, so that branch is gone: an unrestored tree is a refusal, and the only
// thing that runs at commit time is the read-only check.
export function runSync(repoRoot: string, config: Config): number {
  // Deliberately duplicated: the CLI evaluates this BEFORE resolving the work
  // tree, so that D1 is reachable in any directory and can be tested there. This
  // second call guards the function itself, whose arguments a caller has already
  // had to resolve — removing it would make D1 depend on going through the CLI.
  // Both are idempotent; neither is redundant with the other.
  refuseInHookContext('sync');

  const vendorAbs = join(repoRoot, config.vendorDir);
  if (!config.enabled) return outcomeWhenOff(config, vendorAbs, repoRoot);

  const plan = buildPlan(repoRoot, config);

  if (plan.preconditionReasons.length > 0) {
    refusal(`skills-sync sync: dependencies for '${config.runtimeName}' are not restored in '${repoRoot}'.`);
    for (const r of plan.preconditionReasons) console.error(`   - ${r}`);
    console.error('   The writer never publishes a partial vendored tree. Restore dependencies, then run it again.');
    return EXIT_PRECONDITION;
  }

  if (plan.expected.length === 0 && config.requireSubjects) {
    refusal(
      `skills-sync sync: no vendored skill resolved for runtime '${config.runtimeName}' in '${repoRoot}'. ` +
        `Writing an empty tree here would silently remove whatever is committed. ` +
        `If this repository legitimately vendors no skills, declare it: 'requireSubjects: false' in '${config.source}'.`,
    );
    return EXIT_VIOLATION;
  }

  const before = vendorContent(vendorAbs);

  // Staged beside the destination and moved into place, so an interrupted run
  // leaves the committed tree intact rather than half-replaced.
  //
  // Creating that staging directory is the writer's most likely real failure —
  // an unwritable vendor tree — so it gets its own stated refusal. It used to
  // sit outside the try below and escaped as a raw EACCES with a stack trace. A
  // tool whose refusals ARE its product must not answer with an exception dump.
  let staging = '';
  try {
    mkdirSync(dirname(vendorAbs), { recursive: true });
    staging = mkdtempSync(`${vendorAbs}.staging.`);
  } catch (e) {
    throw toolFailure(
      `could not create a staging directory beside '${vendorAbs}': ${(e as Error).message}. ` +
        `The writer builds the new tree next to the old one and moves it into place, so it needs to create ` +
        `directories in '${dirname(vendorAbs)}'.`,
    );
  }

  try {
    writeFileSync(join(staging, KEEP_NAME), '');
    for (const entry of plan.expected) {
      const target = join(staging, entry.path);
      mkdirSync(dirname(target), { recursive: true });
      cpSync(entry.source, target, { dereference: true });
      // Package caches ship read-only files; the vendored copy is ours to
      // rewrite next time.
      chmodSync(target, 0o644);
    }
    writeFileSync(
      join(staging, MANIFEST_NAME),
      renderManifest(buildManifest(config.runtimeName as string, plan.declared, plan.expected)),
    );

    if (existsSync(vendorAbs)) rmSync(vendorAbs, { recursive: true, force: true });
    renameSync(staging, vendorAbs);
  } catch (e) {
    rmSync(staging, { recursive: true, force: true });
    throw toolFailure(`could not write the vendored tree at '${vendorAbs}': ${(e as Error).message}`);
  }

  const after = vendorContent(vendorAbs);
  const added = after.filter(p => !before.includes(p)).length;
  const removed = before.filter(p => !after.includes(p)).length;
  info(`vendored files: ${before.length} → ${after.length} (+${added}/-${removed})`);
  ok(`vendored skills synchronised into '${vendorAbs}' (runtime '${config.runtimeName}'). Commit the result.`);
  return EXIT_OK;
}
