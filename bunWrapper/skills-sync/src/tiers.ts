import { EXIT_OK, EXIT_PRECONDITION, EXIT_VIOLATION, SkillsSyncError } from './exit.ts';
import { refusal, skipped, warn } from './report.ts';

// The three tiers of `skills-sync check`.
//
// They differ on EXACTLY ONE axis: what an unsatisfied PRECONDITION means —
// dependencies that are not restored, or a runtime tool that is not on PATH. A
// tier that behaved the same as its neighbour under that condition would be one
// tier wearing two names, so the table below is the whole design and is written
// as data rather than scattered through branches.
//
// Everything else — drift, an invalid configuration, a vacuous subject set, a
// tool failure — means the same thing in all three tiers. Only the ENVIRONMENT
// verdict is tiered; never the REPOSITORY verdict.

export type Tier = 'setup' | 'pre-commit' | 'ci';

export const TIERS: Tier[] = ['setup', 'pre-commit', 'ci'];

export type PreconditionPolicy = 'refuse-precondition' | 'skip-warning' | 'refuse-violation';

interface TierBehaviour {
  policy: PreconditionPolicy;
  // One line, printed by `check --help`, so the difference is documented where
  // the operator reads it and not only where it is implemented.
  summary: string;
}

export const TIER_BEHAVIOUR: Record<Tier, TierBehaviour> = {
  setup: {
    policy: 'refuse-precondition',
    summary:
      'setup — REFUSES (exit 3) when dependencies are not restored. Setup owns restoring them, so an unrestored tree there is a broken setup, not an excuse.',
  },
  'pre-commit': {
    policy: 'skip-warning',
    summary:
      'pre-commit — WARNING TIER. SKIPS (exit 0) when dependencies are not restored, because a commit must not require a restored dependency tree. This tier is NOT the guarantee.',
  },
  ci: {
    policy: 'refuse-violation',
    summary:
      'ci — THE GUARANTEE. REFUSES (exit 1) when dependencies are not restored. Never skipped, never conditional: CI is where the dependencies are in.',
  },
};

export function parseTier(value: string): Tier {
  if ((TIERS as string[]).includes(value)) return value as Tier;
  throw new SkillsSyncError(
    2,
    `unknown tier '${value}'; 'skills-sync check' takes --tier with one of: ${TIERS.join(', ')}`,
  );
}

// Applies the tier policy to an unsatisfied precondition and returns the exit
// code the run ends with. It reports here rather than throwing so that the three
// outcomes read as three outcomes in the output, not as one error funnel.
export function applyPrecondition(tier: Tier, subject: string, reasons: string[]): number {
  const detail = reasons.map(r => `   - ${r}`).join('\n');
  const behaviour = TIER_BEHAVIOUR[tier];

  switch (behaviour.policy) {
    case 'skip-warning':
      skipped(`skills-sync check --tier pre-commit: dependencies for ${subject} are not restored, so this tier skips.`);
      console.log(detail);
      // D2 forbids this tier from ever calling itself the guarantee. Saying so
      // in its own output is the cheapest place for that to stay true.
      warn(
        'This is the WARNING TIER, not the guarantee. The guarantee is CI: `skills-sync check --tier ci`, which refuses under this same condition.',
      );
      return EXIT_OK;

    case 'refuse-precondition':
      refusal(`skills-sync check --tier setup: dependencies for ${subject} are not restored.`);
      console.error(detail);
      console.error(
        '   Setup restores dependencies and then synchronises strictly; it does not run against an unrestored tree.',
      );
      return EXIT_PRECONDITION;

    case 'refuse-violation':
      refusal(`skills-sync check --tier ci: dependencies for ${subject} are not restored.`);
      console.error(detail);
      console.error(
        '   The CI tier is the guarantee (D2): it is never skipped and never conditional. Restore dependencies before this check runs.',
      );
      return EXIT_VIOLATION;
  }
}
