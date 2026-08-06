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
export const configInvalid = (m: string) => new SkillsSyncError(EXIT_CONFIG_INVALID, m);
export const toolFailure = (m: string) => new SkillsSyncError(EXIT_TOOL, m);
