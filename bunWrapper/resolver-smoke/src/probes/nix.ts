import { findFunctionHeader, maskNixTrivia } from './material.ts';
import type { NixProbe } from './types.ts';

function skipTrivia(source: string, start: number): number {
  let index = start;
  while (index < source.length) {
    if (/\s/.test(source[index])) {
      index++;
      continue;
    }
    if (source[index] === '#') {
      const newline = source.indexOf('\n', index);
      return newline === -1 ? source.length : skipTrivia(source, newline + 1);
    }
    if (source.startsWith('/*', index)) {
      const close = source.indexOf('*/', index + 2);
      return close === -1 ? source.length : skipTrivia(source, close + 2);
    }
    break;
  }
  return index;
}

function bodyOpenAfterHeader(source: string, withName?: 'packages' | 'env'): number | null {
  const header = findFunctionHeader(source);
  if (!header) return null;
  let index = skipTrivia(source, header.colonIndex + 1);
  if (withName) {
    const match = source.slice(index).match(new RegExp(`^with\\s+${withName}\\s*;`));
    if (match) index = skipTrivia(source, index + match[0].length);
  }
  return source[index] === '{' ? index : null;
}

function blockOpen(source: string, pattern: RegExp): number | null {
  const code = maskNixTrivia(source);
  const match = pattern.exec(code);
  if (!match || match.index === undefined) return null;
  const relative = match[0].lastIndexOf('{');
  return relative === -1 ? null : match.index + relative;
}

function matchingBrace(source: string, open: number): number | null {
  const code = maskNixTrivia(source);
  let depth = 0;
  for (let index = open; index < code.length; index++) {
    if (code[index] === '{') depth++;
    if (code[index] === '}') {
      depth--;
      if (depth === 0) return index;
    }
  }
  return null;
}

function injectFlake(real: string, sentinel: string): string | null {
  const inputOpen = blockOpen(real, /\binputs\s*=\s*\{/g);
  if (inputOpen === null) return null;
  let child = `${real.slice(0, inputOpen + 1)}\n    ${sentinel}.url = "github:resolver-smoke/${sentinel}";${real.slice(inputOpen + 1)}`;

  const code = maskNixTrivia(child);
  const outputs = /\boutputs\s*=/.exec(code);
  if (!outputs || outputs.index === undefined) return null;
  const argsOpen = code.indexOf('{', outputs.index + outputs[0].length);
  if (argsOpen === -1) return null;
  const argsClose = matchingBrace(child, argsOpen);
  if (argsClose === null) return null;

  let after = argsClose + 1;
  while (after < code.length && /\s/.test(code[after])) after++;
  if (code[after] === '@') {
    after++;
    while (after < code.length && /[a-zA-Z0-9_'-]/.test(code[after])) after++;
    while (after < code.length && /\s/.test(code[after])) after++;
  }
  if (code[after] !== ':') return null;

  child = `${child.slice(0, argsOpen + 1)}\n      ${sentinel},${child.slice(argsOpen + 1)}`;
  return child.includes(`${sentinel}.url`) && child.includes(`${sentinel},`) ? child : null;
}

function injectEnv(real: string, sentinel: string): string | null {
  const open = bodyOpenAfterHeader(real, 'packages');
  if (open === null) return null;
  const child = `${real.slice(0, open + 1)}\n  ${sentinel} = [\n    git\n  ];${real.slice(open + 1)}`;
  return child.includes(sentinel) ? child : null;
}

function injectFmt(real: string, sentinel: string): string | null {
  const open = blockOpen(real, /\bprograms\s*=\s*\{/g);
  if (open === null) return null;
  const child = `${real.slice(0, open + 1)}\n      ${sentinel}.enable = true;${real.slice(open + 1)}`;
  return child.includes(`${sentinel}.enable`) ? child : null;
}

function injectPackages(real: string, sentinel: string): string | null {
  const code = maskNixTrivia(real);
  const match = /\binherit\b/.exec(code);
  if (!match || match.index === undefined) return null;
  const at = match.index + match[0].length;
  const child = `${real.slice(0, at)}\n          ${sentinel}${real.slice(at)}`;
  return child.includes(sentinel) ? child : null;
}

function injectShells(real: string, sentinel: string): string | null {
  const header = findFunctionHeader(real);
  const open = bodyOpenAfterHeader(real, 'env');
  if (!header || open === null) return null;
  const shellHook = header.args.includes('shellHook') ? '\n    inherit shellHook;' : '';
  const child = `${real.slice(0, open + 1)}\n  ${sentinel} = pkgs.mkShell {\n    buildInputs = [];${shellHook}\n  };${real.slice(open + 1)}`;
  return child.includes(`${sentinel} = pkgs.mkShell`) ? child : null;
}

function injectPrecommit(real: string, sentinel: string): string | null {
  const open = blockOpen(real, /\bhooks\s*=\s*\{/g);
  if (open === null) return null;
  const child = `${real.slice(0, open + 1)}\n    ${sentinel} = {\n      enable = true;\n      name = "${sentinel}";\n      entry = "true";\n      language = "system";\n    };${real.slice(open + 1)}`;
  return child.includes(`${sentinel} =`) ? child : null;
}

// When refreshing vendor/nix.mjs via scripts/refresh-vendor.sh, re-derive
// these skeletons from the refreshed bundle's pretty-printer output.
export const NIX_PROBES: NixProbe[] = [
  {
    path: 'flake.nix',
    emptySkeleton: null,
    inject: injectFlake,
  },
  {
    path: 'nix/env.nix',
    emptySkeleton: ':\n{\n}\n',
    inject: injectEnv,
  },
  {
    path: 'nix/fmt.nix',
    emptySkeleton: ':\nlet\n  fmt = {\n    projectRootFile = "";\n\n    programs = {\n    };\n\n  };\nin\n\n',
    inject: injectFmt,
  },
  {
    path: 'nix/packages.nix',
    emptySkeleton: '{  }:\nlet\n  all = rec {\n  };\nin\nwith all;\n',
    inject: injectPackages,
  },
  {
    path: 'nix/shells.nix',
    emptySkeleton: '{  }:\n{\n}\n',
    inject: injectShells,
  },
  {
    path: 'nix/pre-commit.nix',
    emptySkeleton: null,
    inject: injectPrecommit,
  },
];
