import { expect, test } from 'bun:test';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { main, runLint } from '../src/cli.ts';
import { checkNixpkgsPin } from '../src/nixpkgs-pin.ts';

const rev = '0123456789abcdef0123456789abcdef01234567';

function fixture(change: (lock: any, flake: { text: string }) => void = () => {}): string {
  const root = mkdtempSync(join(tmpdir(), 'dlint-'));
  const lock = {
    nodes: {
      root: { inputs: { 'nixpkgs-main': 'nixpkgs-main_2' } },
      'nixpkgs-main_2': { original: { rev }, locked: { rev } },
    },
  };
  const flake = { text: `inputs.nixpkgs-main.url = "github:NixOS/nixpkgs/${rev}";\n` };
  change(lock, flake);
  writeFileSync(join(root, 'flake.lock'), JSON.stringify(lock));
  writeFileSync(join(root, 'flake.nix'), flake.text);
  return root;
}

test('lint runs every configured check and returns the highest code', () => {
  const result = runLint(
    '.',
    { first: {}, second: {} },
    {
      first: () => ({ code: 1, output: ['failed'] }),
      second: () => ({ code: 4, output: ['reported'] }),
    },
  );
  expect(result.code).toBe(4);
  expect(result.output).toEqual(['first: failed', 'second: reported']);
});

test('lint refuses an empty configuration', () => {
  expect(() => runLint('.', {})).toThrow('no configured checks');
});

test('nixpkgs-pin accepts exact root pins', () => {
  expect(checkNixpkgsPin(fixture(), {}).code).toBe(0);
});

test('lint loads configured checks from dlint.yaml', () => {
  const root = fixture();
  writeFileSync(join(root, 'dlint.yaml'), 'schemaVersion: 1\nchecks:\n  nixpkgs-pin: {}\n');
  expect(main(['lint'], root)).toBe(0);
});

test.each([
  [
    'channel ref',
    (lock: any) => {
      lock.nodes['nixpkgs-main_2'].original.ref = 'nixos-unstable';
    },
    'follows channel',
  ],
  [
    'short revision',
    (lock: any) => {
      lock.nodes['nixpkgs-main_2'].original.rev = '1234';
    },
    'not an exact',
  ],
  [
    'mismatched lock',
    (lock: any) => {
      lock.nodes['nixpkgs-main_2'].locked.rev = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    },
    'asks for',
  ],
  [
    'declared revision not in force',
    (_lock: any, flake: any) => {
      flake.text = 'inputs.nixpkgs-main.url = "github:NixOS/nixpkgs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";\n';
    },
    'declares',
  ],
])('nixpkgs-pin refuses %s', (_name, change, message) => {
  const result = checkNixpkgsPin(fixture(change), {});
  expect(result.code).toBe(1);
  expect(result.output.join('\n')).toContain(message);
});

test('nixpkgs-pin refuses when no root input matches', () => {
  const root = fixture(lock => {
    lock.nodes.root.inputs = { other: 'nixpkgs-main_2' };
  });
  const result = checkNixpkgsPin(root, {});
  expect(result.code).toBe(1);
  expect(result.output.join('\n')).toContain('no root input matches');
});
