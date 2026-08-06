import { loadConfig } from './config.ts';
import { DlintError, usage } from './exit.ts';
import { checkNixpkgsPin, type CheckResult } from './nixpkgs-pin.ts';

type Check = (root: string, config: unknown) => CheckResult;

export const checks: Record<string, Check> = {
  'nixpkgs-pin': checkNixpkgsPin,
};

export function runLint(root: string, configured: Record<string, unknown>, available = checks): CheckResult {
  const allEntries = Object.entries(configured);
  const unknown = allEntries.find(([name]) => available[name] === undefined);
  if (unknown) usage(`lint does not know configured check '${unknown[0]}'`);
  const entries = allEntries.filter(([, value]) => value !== false);
  if (entries.length === 0) usage('lint has no configured checks');
  let code = 0;
  const output: string[] = [];
  for (const [name, config] of entries) {
    let result: CheckResult;
    try {
      result = available[name](root, config);
    } catch (error) {
      result =
        error instanceof DlintError
          ? { code: error.code, output: [error.message] }
          : { code: 5, output: [(error as Error).message] };
    }
    code = Math.max(code, result.code);
    output.push(...result.output.map(line => `${name}: ${line}`));
  }
  return { code, output };
}

export function main(args: string[], root = process.cwd()): number {
  if (args[0] === '--help' || args[0] === '-h') {
    console.log('Usage: dlint lint | dlint nixpkgs-pin');
    return 0;
  }
  try {
    const config = loadConfig(root);
    const result =
      args.length === 1 && args[0] === 'lint'
        ? runLint(root, config.checks)
        : args.length === 1 && args[0] === 'nixpkgs-pin'
          ? checks['nixpkgs-pin'](root, config.checks['nixpkgs-pin'])
          : usage('expected lint or nixpkgs-pin');
    for (const line of result.output) console.log(line);
    return result.code;
  } catch (error) {
    if (error instanceof DlintError) {
      console.error(error.message);
      return error.code;
    }
    console.error((error as Error).message);
    return 5;
  }
}
