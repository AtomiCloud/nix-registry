import manifest from '../package.json' with { type: 'json' };

/**
 * package.json is the single source of the version — `default.nix` reads the same
 * file to name the derivation. It lives in its own module because both `cli.ts`
 * (for `--help` / `--version`) and `run.ts` (for the `--json` report) need it, and
 * importing `cli.ts` from `run.ts` would be a cycle.
 */
export const VERSION: string = (manifest as { version: string }).version;
