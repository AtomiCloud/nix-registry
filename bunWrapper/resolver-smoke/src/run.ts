import { randomBytes } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Config } from './config.ts';
import { EXIT_OK, EXIT_TOOL, EXIT_VIOLATION } from './exit.ts';
import { describeUnit, inventoryMaterial } from './probes/material.ts';
import { NIX_PROBES } from './probes/nix.ts';
import type { NixProbe } from './probes/types.ts';
import type { PublishedResolver, Variation } from './vendor.ts';

interface Finding {
  code: number;
  message: string;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function variation(path: string, content: string, layer: number, template: string): Variation {
  return { path, content, origin: { layer, template } };
}

async function invoke(
  resolver: PublishedResolver,
  path: string,
  label: string,
  files: Variation[],
  findings: Finding[],
): Promise<string | null> {
  try {
    const result = await resolver({ files });
    if (!result || result.path !== path || typeof result.content !== 'string') {
      findings.push({
        code: EXIT_TOOL,
        message: `${path} [${label}/result]: published resolver returned an invalid result`,
      });
      return null;
    }
    return result.content;
  } catch (error) {
    findings.push({
      // The probe child is deliberately well-formed for this dispatch path. A
      // published-merger throw therefore proves the repository shape is not
      // accepted by that resolver; it is a compatibility violation, not an
      // internal resolver-smoke failure. Tool failures are reserved for cases
      // where the oracle itself cannot be loaded, read, or interpreted.
      code: EXIT_VIOLATION,
      message: `${path} [${label}/merge]: published resolver refused well-formed probe input: ${errorMessage(error)}`,
    });
    return null;
  }
}

function addOutputFindings(
  probe: NixProbe,
  label: string,
  output: string,
  expectedInputs: string[],
  findings: Finding[],
  sentinel?: string,
): number {
  const expectedInventories = expectedInputs.map(inventoryMaterial);
  const expectedUnits = new Set(expectedInventories.flatMap(inventory => [...inventory.units]));
  const largestInput = Math.max(...expectedInventories.map(inventory => inventory.units.size));
  const actual = inventoryMaterial(output);
  const isSkeleton = probe.emptySkeleton !== null && output.trimEnd() === probe.emptySkeleton.trimEnd();

  if (output.trim().length === 0 || isSkeleton || actual.units.size < largestInput) {
    const detail = isSkeleton
      ? `output is the published resolver's empty skeleton and retained ${actual.units.size} material unit(s), below the larger input's ${largestInput}`
      : `output retained ${actual.units.size} material unit(s), below the larger input's ${largestInput}`;
    findings.push({
      code: EXIT_VIOLATION,
      message: `${probe.path} [${label}/non-degenerate]: ${detail}`,
    });
  }

  const lost = [...expectedUnits].filter(unit => !actual.units.has(unit)).sort();
  if (lost.length > 0) {
    const shown = lost.slice(0, 16).map(describeUnit).join(', ');
    const remainder = lost.length > 16 ? `, plus ${lost.length - 16} more` : '';
    findings.push({
      code: EXIT_VIOLATION,
      message: `${probe.path} [${label}/material-survival]: lost ${shown}${remainder}`,
    });
  }

  if (sentinel !== undefined && !output.includes(sentinel)) {
    findings.push({
      code: EXIT_VIOLATION,
      message: `${probe.path} [${label}/sentinel-survival]: synthetic child binding '${sentinel}' was lost`,
    });
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
): Promise<{ findings: Finding[]; materialUnits: number }> {
  const findings: Finding[] = [];
  const absolute = join(root, probe.path);
  let real = '';
  try {
    real = readFileSync(absolute, 'utf8');
  } catch (error) {
    return {
      findings: [
        {
          code: EXIT_TOOL,
          message: `${probe.path} [read/tool]: could not read '${absolute}': ${errorMessage(error)}`,
        },
      ],
      materialUnits: 0,
    };
  }

  const sentinel = freshSentinel(real);
  const child = probe.inject(real, sentinel);
  if (child === null || child === real || !child.includes(sentinel)) {
    return {
      findings: [
        {
          code: EXIT_TOOL,
          message: `${probe.path} [synthetic-child/tool]: structure-aware probe could not synthesize a distinct well-formed child; refusing to pass an unproved file`,
        },
      ],
      materialUnits: inventoryMaterial(real).units.size,
    };
  }

  let materialUnits = 0;
  const self = await invoke(resolver, probe.path, 'self-probe', [variation(probe.path, real, 0, 'repo')], findings);
  if (self !== null) {
    materialUnits = Math.max(materialUnits, addOutputFindings(probe, 'self-probe', self, [real], findings));
  }

  const realVariation = variation(probe.path, real, 0, 'repo');
  const childVariation = variation(probe.path, child, 1, 'resolver-smoke-child');
  const merged = await invoke(resolver, probe.path, 'synthetic-child', [realVariation, childVariation], findings);
  if (merged !== null) {
    materialUnits = Math.max(
      materialUnits,
      addOutputFindings(probe, 'synthetic-child', merged, [real, child], findings, sentinel),
    );
  }

  const reversed = await invoke(resolver, probe.path, 'input-order-control', [childVariation, realVariation], findings);
  if (merged !== null && reversed !== null && merged !== reversed) {
    findings.push({
      code: EXIT_VIOLATION,
      message: `${probe.path} [input-order-control/call-order]: reversing caller array order while preserving origin metadata changed the merged bytes`,
    });
  }

  return { findings, materialUnits };
}

export async function runSmoke(root: string, config: Config, resolver: PublishedResolver): Promise<number> {
  const started = performance.now();
  const present = NIX_PROBES.filter(probe => existsSync(join(root, probe.path)));
  if (present.length === 0) {
    if (!config.requireSubjects) {
      console.log(
        '⏭️ resolver-smoke: 0 resolver-managed files; absence is explicitly declared by requireSubjects: false',
      );
      return EXIT_OK;
    }
    console.error(
      "❌ resolver-smoke [subjects/non-vacuity]: no resolver-managed files were found; declare 'requireSubjects: false' in resolver-smoke.yaml only when that absence is intentional",
    );
    return EXIT_VIOLATION;
  }

  const allFindings: Finding[] = [];
  let passed = 0;
  for (const probe of present) {
    const result = await probeFile(root, probe, resolver);
    allFindings.push(...result.findings);
    if (result.findings.length === 0) {
      passed++;
      console.log(
        `✅ ${probe.path}: resolver probe passed (self-merge, synthetic sentinel, ${result.materialUnits} material unit(s))`,
      );
    }
  }

  for (const finding of allFindings) console.error(`❌ ${finding.message}`);
  const elapsed = Math.round(performance.now() - started);
  if (allFindings.length > 0) {
    console.error(
      `❌ resolver-smoke: ${allFindings.length} refusal(s) across ${present.length} resolver-managed file(s) in ${elapsed}ms`,
    );
    return Math.max(...allFindings.map(finding => finding.code));
  }

  console.log(`✅ resolver-smoke: ${passed} resolver-managed file(s) passed in ${elapsed}ms`);
  return EXIT_OK;
}
