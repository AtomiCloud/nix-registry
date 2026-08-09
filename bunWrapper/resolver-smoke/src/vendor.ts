import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { configInvalid, toolFailure } from './exit.ts';

export interface Variation {
  path: string;
  content: string;
  origin: {
    layer: number;
    template: string;
  };
}

export type PublishedResolver = (input: { files: Variation[] }) => Promise<{ path: string; content: string }>;

const VENDOR_DIR = join(import.meta.dir, '..', 'vendor');
export const RECORDED_SHA256_PATH = join(VENDOR_DIR, 'SHA256');
export const DEFAULT_VENDOR_PATH = join(VENDOR_DIR, 'nix-v2.mjs');

function readRecordedDigest(): string {
  let digest = '';
  try {
    digest = readFileSync(RECORDED_SHA256_PATH, 'utf8').trim();
  } catch (error) {
    throw configInvalid(
      `vendored resolver integrity file '${RECORDED_SHA256_PATH}' could not be read: ${(error as Error).message}`,
    );
  }
  if (!/^[0-9a-f]{64}$/.test(digest)) {
    throw configInvalid(
      `vendored resolver integrity file '${RECORDED_SHA256_PATH}' must contain one lowercase sha256, found '${digest}'`,
    );
  }
  return digest;
}

function selectedVendorPath(): string {
  const override = process.env.RESOLVER_SMOKE_VENDOR;
  if (!override) return DEFAULT_VENDOR_PATH;
  return isAbsolute(override) ? override : resolve(process.cwd(), override);
}

export async function loadPublishedResolver(): Promise<PublishedResolver> {
  const expected = readRecordedDigest();
  const vendorPath = selectedVendorPath();

  let bytes: Buffer;
  try {
    bytes = readFileSync(vendorPath);
  } catch (error) {
    throw configInvalid(`vendored resolver '${vendorPath}' could not be read: ${(error as Error).message}`);
  }
  const actual = createHash('sha256').update(bytes).digest('hex');
  if (actual !== expected) {
    throw configInvalid(
      `vendored resolver '${vendorPath}' failed integrity: expected sha256 ${expected}, actual sha256 ${actual}; refresh it with scripts/refresh-vendor.sh, never at hook time`,
    );
  }

  let module: Record<string, unknown>;
  try {
    module = (await import(pathToFileURL(vendorPath).href)) as Record<string, unknown>;
  } catch (error) {
    throw toolFailure(`could not import hash-verified published resolver '${vendorPath}': ${(error as Error).message}`);
  }

  const exports = Object.keys(module).sort();
  if (exports.length !== 1 || exports[0] !== 'resolver' || typeof module.resolver !== 'function') {
    throw configInvalid(
      `hash-verified vendored resolver '${vendorPath}' has unexpected exports [${exports.join(', ')}]; expected exactly [resolver]`,
    );
  }
  return module.resolver as PublishedResolver;
}
