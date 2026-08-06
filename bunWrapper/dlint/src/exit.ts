export class DlintError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message);
  }
}

export const refuse = (message: string): never => {
  throw new DlintError(1, message);
};

export const usage = (message: string): never => {
  throw new DlintError(2, message);
};
