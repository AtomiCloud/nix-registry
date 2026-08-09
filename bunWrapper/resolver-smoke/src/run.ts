import { randomBytes } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Config } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_VIOLATION } from './exit.ts';
import {
  CHILD_TEMPLATE,
  type LossDetail,
  OWN_DISCLOSURE_CAP,
  ownLossDetail,
  PUBLISHED_DISCLOSURE_CAP,
  publishedLossDetail,
} from './loss.ts';
import {
  describeMaterialUnit,
  describeUnit,
  inventoryMaterial,
  type MaterialUnit,
  splitUnitKey,
} from './probes/material.ts';
import { NIX_PROBES } from './probes/nix.ts';
import type { NixProbe } from './probes/types.ts';
import type { PublishedResolver, Variation } from './vendor.ts';
import { VERSION } from './version.ts';

export interface RunOptions {
  /** Print every lost unit and every disclosure block instead of truncating. */
  full: boolean;
  /** Emit one machine-readable report and suppress every human line. */
  json: boolean;
  vendor: { path: string; sha256: string };
}

interface Finding {
  /** null only for the run-wide subjects finding, which names no file. */
  path: string | null;
  arm: string;
  check: string;
  code: number;
  message: string;
  /** Rendering companion for the loss-detail lines; never a finding itself. */
  loss: LossDetail | null;
  /** How many probe inputs the arm this finding came from was given. */
  inputs: number;
}

type ArmOutcome = 'merged' | 'refused' | 'invalid-result';

interface ArmReport {
  arm: string;
  inputs: number;
  outcome: ArmOutcome;
  loss: LossDetail | null;
}

interface FileReport {
  path: string;
  passed: boolean;
  materialUnits: number;
  arms: ArmReport[];
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function variation(path: string, content: string, layer: number, template: string): Variation {
  return { path, content, origin: { layer, template } };
}

function finding(
  path: string | null,
  arm: string,
  check: string,
  code: number,
  detail: string,
  inputs = 0,
  loss: LossDetail | null = null,
): Finding {
  return { path, arm, check, code, message: `${path ?? 'resolver-smoke'} [${arm}/${check}]: ${detail}`, loss, inputs };
}

async function invoke(
  resolver: PublishedResolver,
  path: string,
  label: string,
  files: Variation[],
  findings: Finding[],
  arms: ArmReport[],
): Promise<string | null> {
  const arm: ArmReport = { arm: label, inputs: files.length, outcome: 'merged', loss: null };
  arms.push(arm);
  try {
    const result = await resolver({ files });
    if (!result || result.path !== path || typeof result.content !== 'string') {
      arm.outcome = 'invalid-result';
      findings.push(
        finding(path, label, 'result', EXIT_TOOL, 'published resolver returned an invalid result', files.length),
      );
      return null;
    }
    return result.content;
  } catch (error) {
    arm.outcome = 'refused';
    const reason = errorMessage(error);
    // The published loss guard discards the merged output when it throws, so the
    // names past its 24-unit cap cannot be recovered here. What CAN be recovered
    // is the complete disclosed set, this arm's synthetic-child contribution and
    // a provable upper bound on the remainder — see src/loss.ts. A refusal that
    // is not the loss-guard shape gets no detail at all rather than a guess.
    arm.loss = publishedLossDetail(
      path,
      reason,
      files.map(file => file.content),
      files.filter(file => file.origin.template === CHILD_TEMPLATE).map(file => file.content),
    );
    findings.push(
      finding(
        path,
        label,
        'merge',
        // The probe child is deliberately well-formed for this dispatch path. A
        // published-merger throw therefore proves the repository shape is not
        // accepted by that resolver; it is a compatibility violation, not an
        // internal resolver-smoke failure. Tool failures are reserved for cases
        // where the oracle itself cannot be loaded, read, or interpreted.
        EXIT_VIOLATION,
        `published resolver refused well-formed probe input: ${reason}`,
        files.length,
        arm.loss,
      ),
    );
    return null;
  }
}

function addOutputFindings(
  probe: NixProbe,
  label: string,
  output: string,
  expectedInputs: Variation[],
  findings: Finding[],
  arm: ArmReport | undefined,
  options: RunOptions,
  sentinel?: string,
): number {
  const sources = expectedInputs.map(input => input.content);
  const expectedInventories = sources.map(inventoryMaterial);
  const expectedUnits = new Set(expectedInventories.flatMap(inventory => [...inventory.units]));
  const largestInput = Math.max(...expectedInventories.map(inventory => inventory.units.size));
  const actual = inventoryMaterial(output);
  const isSkeleton = probe.emptySkeleton !== null && output.trimEnd() === probe.emptySkeleton.trimEnd();

  if (output.trim().length === 0 || isSkeleton || actual.units.size < largestInput) {
    const detail = isSkeleton
      ? `output is the published resolver's empty skeleton and retained ${actual.units.size} material unit(s), below the larger input's ${largestInput}`
      : `output retained ${actual.units.size} material unit(s), below the larger input's ${largestInput}`;
    findings.push(finding(probe.path, label, 'non-degenerate', EXIT_VIOLATION, detail, expectedInputs.length));
  }

  const lost = [...expectedUnits].filter(unit => !actual.units.has(unit)).sort();
  if (lost.length > 0) {
    // The list order stays the plain key sort it has always been rather than
    // adopting the bundle's comparator: this text is asserted by substring in the
    // harness, and reordering it would change default output for no gain. The
    // bundle's comparator is exported for the candidate-remainder claim, which is
    // the one computation that has to agree with the bundle unit for unit.
    const truncated = !options.full && lost.length > OWN_DISCLOSURE_CAP;
    const shown = (truncated ? lost.slice(0, OWN_DISCLOSURE_CAP) : lost).map(describeUnit).join(', ');
    // `, plus N more` is kept byte-identical; the semantics are appended after it
    // because the bare count read as occurrences to the one specification that
    // ever tried to use this list, and it guessed its own subject matter wrong.
    const remainder = truncated
      ? `, plus ${lost.length - OWN_DISCLOSURE_CAP} more — ${lost.length} DISTINCT (kind, name) unit(s) deduplicated across this arm's ${expectedInputs.length} probe input(s), not occurrences; showing the first ${OWN_DISCLOSURE_CAP}, re-run with --full or --json for all of them`
      : '';
    const detail = ownLossDetail(
      lost.map(splitUnitKey),
      sources,
      expectedInputs.filter(input => input.origin.template === CHILD_TEMPLATE).map(input => input.content),
    );
    if (arm) arm.loss = detail;
    findings.push(
      finding(
        probe.path,
        label,
        'material-survival',
        EXIT_VIOLATION,
        `lost ${shown}${remainder}`,
        expectedInputs.length,
        detail,
      ),
    );
  }

  if (sentinel !== undefined && !output.includes(sentinel)) {
    findings.push(
      finding(
        probe.path,
        label,
        'sentinel-survival',
        EXIT_VIOLATION,
        `synthetic child binding '${sentinel}' was lost`,
        expectedInputs.length,
      ),
    );
  }
  return actual.units.size;
}

function freshSentinel(real: string): string {
  let sentinel = '';
  do {
    sentinel = `resolverSmokeSentinel_${randomBytes(6).toString('hex')}`;
  } while (real.includes(sentinel));
  return sentinel;
}

async function probeFile(
  root: string,
  probe: NixProbe,
  resolver: PublishedResolver,
  options: RunOptions,
): Promise<{ findings: Finding[]; report: FileReport }> {
  const findings: Finding[] = [];
  const arms: ArmReport[] = [];
  const report: FileReport = { path: probe.path, passed: false, materialUnits: 0, arms };
  const absolute = join(root, probe.path);
  let real = '';
  try {
    real = readFileSync(absolute, 'utf8');
  } catch (error) {
    return {
      findings: [
        finding(probe.path, 'read', 'tool', EXIT_TOOL, `could not read '${absolute}': ${errorMessage(error)}`),
      ],
      report,
    };
  }

  const sentinel = freshSentinel(real);
  const child = probe.inject(real, sentinel);
  if (child === null || child === real || !child.includes(sentinel)) {
    report.materialUnits = inventoryMaterial(real).units.size;
    return {
      findings: [
        finding(
          probe.path,
          'synthetic-child',
          'tool',
          EXIT_TOOL,
          'structure-aware probe could not synthesize a distinct well-formed child; refusing to pass an unproved file',
        ),
      ],
      report,
    };
  }

  let materialUnits = 0;
  const realVariation = variation(probe.path, real, 0, 'repo');
  const childVariation = variation(probe.path, child, 1, CHILD_TEMPLATE);

  const self = await invoke(resolver, probe.path, 'self-probe', [realVariation], findings, arms);
  if (self !== null) {
    materialUnits = Math.max(
      materialUnits,
      addOutputFindings(probe, 'self-probe', self, [realVariation], findings, arms.at(-1), options),
    );
  }

  const merged = await invoke(resolver, probe.path, 'synthetic-child', [realVariation, childVariation], findings, arms);
  if (merged !== null) {
    materialUnits = Math.max(
      materialUnits,
      addOutputFindings(
        probe,
        'synthetic-child',
        merged,
        [realVariation, childVariation],
        findings,
        arms.at(-1),
        options,
        sentinel,
      ),
    );
  }

  const reversed = await invoke(
    resolver,
    probe.path,
    'input-order-control',
    [childVariation, realVariation],
    findings,
    arms,
  );
  if (merged !== null && reversed !== null && merged !== reversed) {
    findings.push(
      finding(
        probe.path,
        'input-order-control',
        'call-order',
        EXIT_VIOLATION,
        'reversing caller array order while preserving origin metadata changed the merged bytes',
        2,
      ),
    );
  }

  report.materialUnits = materialUnits;
  report.passed = findings.length === 0;
  return { findings, report };
}

// ---------------------------------------------------------------------------
// Rendering. The loss-detail lines below are informational and are written to
// stderr with an `ℹ️` prefix. They are NOT findings: they never enter
// allFindings, never change the `N refusal(s)` count and never change the exit
// code. A run's verdict is decided by the refusals alone, and a disclosure that
// could move the verdict would make every operator who wanted more detail pay
// for it in a different answer.
// ---------------------------------------------------------------------------

const INDENT = '   ';
const UNIT_INDENT = '     ';

/**
 * Hard-wrap at 96 columns with a three-space continuation indent. Wrapped here
 * rather than hand-split in the template because the counts vary in width: a
 * hand-wrapped literal goes ragged the first time one of them crosses ten.
 */
function wrapInfo(text: string): string[] {
  const lines: string[] = [];
  let line = '';
  for (const word of text.split(' ')) {
    const candidate = line.length === 0 ? word : `${line} ${word}`;
    if (line.length > 0 && candidate.length > 96) {
      lines.push(line);
      line = `${INDENT}${word}`;
      continue;
    }
    line = candidate;
  }
  if (line.length > 0) lines.push(line);
  return lines;
}

function renderUnitBlock(header: string, units: MaterialUnit[], emptyNote: string): void {
  if (units.length === 0) {
    console.error(`${INDENT}${header}: ${emptyNote}`);
    return;
  }
  console.error(`${INDENT}${header}:`);
  for (const unit of units) console.error(`${UNIT_INDENT}${describeMaterialUnit(unit)}`);
}

function renderLossDetail(entry: Finding, options: RunOptions): void {
  const detail = entry.loss;
  if (detail === null || detail.source !== 'published-loss-guard') return;

  const closing = options.full
    ? "The complete disclosed set, this arm's synthetic-child contribution and the candidate remainder follow."
    : "Re-run with --full or --json for the complete disclosed set, this arm's synthetic-child contribution and the candidate remainder.";
  const summary =
    `ℹ️ ${entry.path} [${entry.arm}/loss-detail]: the published resolver disclosed ` +
    `${detail.disclosedCount} lost unit(s) and withheld ${detail.withheldCount} more ` +
    `(${detail.totalLostCount} total). Counts are DISTINCT (kind, name) units deduplicated ` +
    `across this arm's ${entry.inputs} probe input(s), not occurrences. ${closing}`;
  for (const line of wrapInfo(summary)) console.error(line);
  if (!options.full) return;

  renderUnitBlock(`disclosed — ${detail.disclosedCount} unit(s)`, detail.disclosed, 'none');
  renderUnitBlock(
    `child-contributed — ${detail.childContributed.length} unit(s)`,
    detail.childContributed,
    entry.inputs < 2
      ? 'none — this arm has a single probe input, so no synthetic child could contribute'
      : 'none — the synthetic child introduced no guarded unit the real file does not already have',
  );
  // The "provably inside" clause is dropped when the set is empty: there is no
  // withheld name to be inside it, and a bound stated over nothing reads as
  // though something were still hidden.
  const bound = `candidate (upper bound) — ${detail.candidateRemainder.length} unit(s)`;
  renderUnitBlock(
    detail.candidateRemainder.length === 0 ? bound : `${bound}; every withheld name is provably inside this set`,
    detail.candidateRemainder,
    'none — nothing was withheld, so there is no remainder to bound',
  );
  if (!detail.remainderDetermined) return;
  console.error(
    detail.withheldCount === 0
      ? `${INDENT}remainder DETERMINED: nothing was withheld — the disclosed set above is the complete lost set`
      : `${INDENT}remainder DETERMINED: the withheld names are exactly the ${detail.candidateRemainder.length} candidate(s) above`,
  );
}

function unitJson(units: MaterialUnit[]): { kind: string; name: string }[] {
  return units.map(unit => ({ kind: unit.kind, name: unit.name }));
}

function lossJson(detail: LossDetail | null): unknown {
  if (detail === null) return null;
  return {
    source: detail.source,
    disclosed: unitJson(detail.disclosed),
    disclosedCount: detail.disclosedCount,
    withheldCount: detail.withheldCount,
    totalLostCount: detail.totalLostCount,
    childContributed: unitJson(detail.childContributed),
    candidateRemainder: unitJson(detail.candidateRemainder),
    remainderDetermined: detail.remainderDetermined,
  };
}

function emitJson(
  root: string,
  config: Config,
  options: RunOptions,
  files: FileReport[],
  findings: Finding[],
  elapsed: number,
  exitCode: number,
): void {
  console.log(
    JSON.stringify(
      {
        tool: 'resolver-smoke',
        version: VERSION,
        root,
        vendor: options.vendor,
        config: { source: config.source, requireSubjects: config.requireSubjects },
        countSemantics: {
          unit: 'distinct (kind, name) pair',
          dedupedAcross: 'every probe input of the arm',
          countsOccurrences: false,
          syntheticChildContributes: true,
          publishedDisclosureCap: PUBLISHED_DISCLOSURE_CAP,
          ownDisclosureCap: OWN_DISCLOSURE_CAP,
        },
        files: files.map(file => ({
          path: file.path,
          passed: file.passed,
          materialUnits: file.materialUnits,
          arms: file.arms.map(arm => ({
            arm: arm.arm,
            inputs: arm.inputs,
            outcome: arm.outcome,
            loss: lossJson(arm.loss),
          })),
        })),
        findings: findings.map(entry => ({
          path: entry.path,
          arm: entry.arm,
          check: entry.check,
          code: entry.code,
          message: entry.message,
        })),
        summary: {
          files: files.length,
          passed: files.filter(file => file.passed).length,
          refusals: findings.length,
          elapsedMs: elapsed,
          exitCode,
        },
      },
      null,
      2,
    ),
  );
}

export async function runSmoke(
  root: string,
  config: Config,
  resolver: PublishedResolver,
  options: RunOptions,
): Promise<number> {
  const started = performance.now();
  const elapsedMs = () => Math.round(performance.now() - started);
  const present = NIX_PROBES.filter(probe => existsSync(join(root, probe.path)));

  if (present.length === 0) {
    if (!config.requireSubjects) {
      if (options.json) {
        emitJson(root, config, options, [], [], elapsedMs(), EXIT_OK);
        return EXIT_OK;
      }
      console.log(
        '⏭️ resolver-smoke: 0 resolver-managed files; absence is explicitly declared by requireSubjects: false',
      );
      return EXIT_OK;
    }
    const vacuous = finding(
      null,
      'subjects',
      'non-vacuity',
      EXIT_VIOLATION,
      "no resolver-managed files were found; declare 'requireSubjects: false' in resolver-smoke.yaml only when that absence is intentional",
    );
    if (options.json) {
      emitJson(root, config, options, [], [vacuous], elapsedMs(), EXIT_VIOLATION);
      return EXIT_VIOLATION;
    }
    console.error(`❌ ${vacuous.message}`);
    return EXIT_VIOLATION;
  }

  const allFindings: Finding[] = [];
  const files: FileReport[] = [];
  let passed = 0;
  for (const probe of present) {
    const result = await probeFile(root, probe, resolver, options);
    allFindings.push(...result.findings);
    files.push(result.report);
    if (result.findings.length === 0) {
      passed++;
      if (!options.json) {
        console.log(
          `✅ ${probe.path}: resolver probe passed (self-merge, synthetic sentinel, ${result.report.materialUnits} material unit(s))`,
        );
      }
    }
  }

  const exitCode = allFindings.length > 0 ? Math.max(...allFindings.map(entry => entry.code)) : EXIT_OK;
  if (options.json) {
    emitJson(root, config, options, files, allFindings, elapsedMs(), exitCode);
    return exitCode;
  }

  for (const entry of allFindings) {
    console.error(`❌ ${entry.message}`);
    renderLossDetail(entry, options);
  }
  const elapsed = elapsedMs();
  if (allFindings.length > 0) {
    console.error(
      `❌ resolver-smoke: ${allFindings.length} refusal(s) across ${present.length} resolver-managed file(s) in ${elapsed}ms`,
    );
    return exitCode;
  }

  console.log(`✅ resolver-smoke: ${passed} resolver-managed file(s) passed in ${elapsed}ms`);
  return EXIT_OK;
}
