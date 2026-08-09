export const EXIT_OK = 0;
export const EXIT_VIOLATION = 1;
export const EXIT_USAGE = 2;
export const EXIT_CONFIG_INVALID = 4;
export const EXIT_TOOL = 5;

export class ResolverSmokeError extends Error {
  readonly code: number;

  constructor(code: number, message: string) {
    super(message);
    this.code = code;
  }
}

export const usageError = (message: string) => new ResolverSmokeError(EXIT_USAGE, message);
export const configInvalid = (message: string) => new ResolverSmokeError(EXIT_CONFIG_INVALID, message);
export const toolFailure = (message: string) => new ResolverSmokeError(EXIT_TOOL, message);
