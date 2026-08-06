// Exit codes are part of the tool's contract: a caller (a hook, a CI job, a
// setup script) branches on them, so they are named here once and never
// invented at a call site.
//
// The families mirror dlint's, deliberately — the two tools are read side by
// side by the same reviewers — with one addition: PRECONDITION. A precondition
// is the one condition whose HANDLING differs per tier (see tiers.ts); every
// other outcome means the same thing at setup, at pre-commit and in CI.
export const EXIT_OK = 0;
export const EXIT_VIOLATION = 1;
export const EXIT_USAGE = 2;
export const EXIT_PRECONDITION = 3;
export const EXIT_CONFIG_INVALID = 4;
export const EXIT_TOOL = 5;

export class SkillsSyncError extends Error {
  readonly code: number;
  constructor(code: number, message: string) {
    super(message);
    this.code = code;
  }
}

export const usageError = (m: string) => new SkillsSyncError(EXIT_USAGE, m);
export const violation = (m: string) => new SkillsSyncError(EXIT_VIOLATION, m);
export const precondition = (m: string) => new SkillsSyncError(EXIT_PRECONDITION, m);
export const configInvalid = (m: string) => new SkillsSyncError(EXIT_CONFIG_INVALID, m);
export const toolFailure = (m: string) => new SkillsSyncError(EXIT_TOOL, m);
