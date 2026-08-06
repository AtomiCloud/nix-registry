import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { type Config, KEEP_NAME, MANIFEST_NAME } from './config.ts';
import { sha256 } from './engine.ts';
import { EXIT_OK, EXIT_PRECONDITION, EXIT_VIOLATION, toolFailure } from './exit.ts';
import { stagedSha256 } from './git.ts';
import { buildManifest, renderManifest } from './manifest.ts';
import { buildPlan, outcomeWhenOff, vendorContent } from './plan.ts';
import { info, ok, refusal, skipped, warn } from './report.ts';
import type { Tier } from './tiers.ts';

// `skills-sync sync` is the WRITER, and under the owner's amendment it runs at
// setup, pre-commit AND CI. The never-in-hooks clause is revoked; everything
// else in the D-group stands, and D11 now does MORE work because two more places
// write the tree.
//
// The shape at commit time is treefmt's, ruled by norah: ANY tree mutation FAILS
// the commit loudly, with the regenerated files left in the working tree for the
// user to stage. THE TOOL NEVER STAGES ANYTHING — a hook that silently amends
// the commit is not acceptable, because a defect repaired every cycle presents
// as no defect at all.

export interface WriteOutcome {
  mutated: string[];
  indexStale: string[];
}

// CONTENT-IDEMPOTENT WRITE.
//
// Only files whose CONTENT differs are touched. That is load-bearing rather than
// tidy: the commit-time rule is "any mutation is loud", so the writer has to be
// able to answer "did I change anything" truthfully. The previous writer always
// replaced the whole directory, so it could not distinguish "I changed something"
// from "I rewrote identical bytes" — under this rule it would report a mutation
// on EVERY commit and reject A2, the commit that fixes the tree.
//
// And the detector is keyed on CONTENT, not on mtime and not on "did I replace
// the directory". Measured: a content-identical sync churns inode and mtime
// while `git status` stays empty. An mtime-keyed or replacement-keyed detector
// therefore FAILS A2; a content-keyed one passes it.
function writeVendorTree(vendorAbs: string, config: Config, plan: ReturnType<typeof buildPlan>): string[] {
  const mutated: string[] = [];
  try {
    mkdirSync(vendorAbs, { recursive: true });
    if (!existsSync(join(vendorAbs, KEEP_NAME))) writeFileSync(join(vendorAbs, KEEP_NAME), '');

    const expectedPaths = new Set(plan.expected.map(e => e.path));
    for (const entry of plan.expected) {
      const target = join(vendorAbs, entry.path);
      if (existsSync(target) && sha256(target) === entry.sha256) continue;
      mkdirSync(dirname(target), { recursive: true });
      // copyFileSync, NOT cpSync: `cpSync(src, dst, { dereference: true })`
      // SILENTLY DOES NOT OVERWRITE an existing destination on this runtime — no
      // throw, the old bytes remain. A writer that silently does not write is a
      // false green by construction, and the staging-directory writer never met
      // it only because its targets never existed.
      copyFileSync(entry.source, target);
      // Package caches ship read-only files; the vendored copy is ours to rewrite.
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

// Which expected files the INDEX does not already carry.
//
// This is the condition a mutation-only rule cannot reach: a user who
// regenerates by hand and does not stage leaves nothing to mutate, so the tree
// is correct on disk, the writer is silent, and the commit still records the
// stale tree — because git commits the index.
function indexDisagreements(repoRoot: string, config: Config, plan: ReturnType<typeof buildPlan>): string[] {
  const stale: string[] = [];
  for (const entry of plan.expected) {
    const tracked = `${config.vendorDir}/${entry.path}`;
    if (stagedSha256(repoRoot, tracked) !== entry.sha256) stale.push(tracked);
  }
  return stale;
}

export function runSync(repoRoot: string, config: Config, tier: Tier | null): number {
  const vendorAbs = join(repoRoot, config.vendorDir);
  if (!config.enabled) return outcomeWhenOff(config, vendorAbs, repoRoot);

  const plan = buildPlan(repoRoot, config);
  info(`tier: ${tier ?? 'manual'}`);

  if (plan.preconditionReasons.length > 0) {
    // The deps-absent skip-guard STANDS. At pre-commit it must SKIP: a commit
    // must never require a restored dependency tree.
    if (tier === 'pre-commit') {
      skipped(`skills-sync sync --tier pre-commit: dependencies are not restored, so this tier skips.`);
      for (const reason of plan.preconditionReasons) console.log(`   - ${reason}`);
      warn(
        'This is the WARNING TIER, not the guarantee. The guarantee is CI, which refuses under this same condition.',
      );
      return EXIT_OK;
    }
    refusal(`skills-sync sync: dependencies for '${config.runtimeName}' are not restored in '${repoRoot}'.`);
    for (const reason of plan.preconditionReasons) console.error(`   - ${reason}`);
    console.error('   The writer never publishes a partial vendored tree. Restore dependencies, then run it again.');
    return tier === 'ci' ? EXIT_VIOLATION : EXIT_PRECONDITION;
  }

  if (plan.expected.length === 0 && config.requireSubjects) {
    refusal(
      `skills-sync sync: no vendored skill resolved for runtime '${config.runtimeName}' in '${repoRoot}'. ` +
        `Writing an empty tree here would silently remove whatever is committed. ` +
        `If this repository legitimately vendors no skills, declare it: 'requireSubjects: false' in '${config.source}'.`,
    );
    return EXIT_VIOLATION;
  }

  const mutated = writeVendorTree(vendorAbs, config, plan);
  info(`writer mutated ${mutated.length} file(s)${mutated.length ? `: ${mutated.slice(0, 5).join(', ')}` : ''}`);

  if (tier === 'pre-commit' || tier === 'ci') {
    const indexStale = indexDisagreements(repoRoot, config, plan);
    info(`index entries disagreeing with the expected tree: ${indexStale.length}`);

    if (mutated.length > 0 || indexStale.length > 0) {
      refusal(`skills-sync sync --tier ${tier}: the vendored tree is not what the packages ship.`);
      if (mutated.length > 0) {
        console.error(`   regenerated ${mutated.length} file(s) in your working tree:`);
        for (const path of mutated.slice(0, 10)) console.error(`     - ${config.vendorDir}/${path}`);
      }
      if (indexStale.length > 0) {
        console.error(`   ${indexStale.length} file(s) are not staged as the packages ship them:`);
        for (const path of indexStale.slice(0, 10)) console.error(`     - ${path}`);
      }
      // The two halves of the rule that must never be softened: nothing was
      // staged, and CI does not repair-and-pass. A self-repairing guarantee is a
      // repair loop nobody sees.
      console.error(`   NOTHING was added to your commit. Stage the vendored tree and commit again.`);
      return EXIT_VIOLATION;
    }
    ok(`vendored tree already matches what the packages ship, and is staged; the commit may proceed.`);
    return EXIT_OK;
  }

  ok(`vendored skills synchronised into '${vendorAbs}' (runtime '${config.runtimeName}'). Commit the result.`);
  return EXIT_OK;
}
