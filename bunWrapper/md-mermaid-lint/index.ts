#!/usr/bin/env bun
/**
 * md-mermaid-lint: Lint markdown files containing mermaid diagrams
 *
 * This tool validates markdown files and checks for common issues in mermaid
 * code blocks.
 */

import { glob } from 'glob';
import { readFileSync, existsSync } from 'fs';
import { join, resolve } from 'path';

const VERSION = '0.1.0';

interface LintResult {
  file: string;
  line: number | null;
  message: string;
  severity: 'error' | 'warning';
}

/**
 * Parse command line arguments
 */
function parseArgs(args: string[]): {
  files: string[];
  help: boolean;
  version: boolean;
} {
  const result = { files: [] as string[], help: false, version: false };

  for (const arg of args) {
    if (arg === '--help' || arg === '-h') {
      result.help = true;
    } else if (arg === '--version' || arg === '-v') {
      result.version = true;
    } else if (!arg.startsWith('-')) {
      result.files.push(arg);
    }
  }

  return result;
}

/**
 * Display help message
 */
function showHelp(): void {
  console.log(`
md-mermaid-lint - Lint markdown files with mermaid diagrams

USAGE:
    md-mermaid-lint [OPTIONS] [FILES...]

OPTIONS:
    -h, --help      Show this help message
    -v, --version   Show version information

EXAMPLES:
    md-mermaid-lint README.md
    md-mermaid-lint docs/**/*.md
    md-mermaid-lint .
`);
}

/**
 * Lint a single markdown file for mermaid diagram issues
 */
function lintFile(filePath: string): LintResult[] {
  const results: LintResult[] = [];

  if (!existsSync(filePath)) {
    results.push({
      file: filePath,
      line: null,
      message: `File not found: ${filePath}`,
      severity: 'error',
    });
    return results;
  }

  const content = readFileSync(filePath, 'utf-8');
  const lines = content.split('\n');

  // Track mermaid code blocks
  let inMermaidBlock = false;
  let mermaidStartLine = 0;

  lines.forEach((line, index) => {
    const lineNum = index + 1;

    // Check for mermaid code block start
    if (line.trim().startsWith('```mermaid')) {
      inMermaidBlock = true;
      mermaidStartLine = lineNum;
    } else if (inMermaidBlock && line.trim() === '```') {
      // End of mermaid block
      inMermaidBlock = false;
    } else if (inMermaidBlock) {
      // Check for common issues inside mermaid blocks
      const trimmedLine = line.trim();

      // Check for empty lines in diagram (usually a mistake)
      if (trimmedLine === '') {
        results.push({
          file: filePath,
          line: lineNum,
          message: 'Empty line inside mermaid diagram block',
          severity: 'warning',
        });
      }
    }
  });

  // Check for unclosed mermaid blocks
  if (inMermaidBlock) {
    results.push({
      file: filePath,
      line: mermaidStartLine,
      message: 'Unclosed mermaid code block',
      severity: 'error',
    });
  }

  return results;
}

/**
 * Main entry point
 */
async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));

  if (args.help) {
    showHelp();
    process.exit(0);
  }

  if (args.version) {
    console.log(`md-mermaid-lint v${VERSION}`);
    process.exit(0);
  }

  // If no files specified, show help
  if (args.files.length === 0) {
    console.error('Error: No files specified');
    showHelp();
    process.exit(1);
  }

  // Expand glob patterns and collect files
  const allResults: LintResult[] = [];
  let errorCount = 0;
  let warningCount = 0;

  for (const pattern of args.files) {
    let files: string[];
    const stats = existsSync(pattern) && (await import('fs')).statSync(pattern);

    if (stats && stats.isDirectory()) {
      // If directory, search for markdown files
      files = await glob(join(pattern, '**/*.md'));
    } else if (stats && stats.isFile()) {
      // If it's a literal file path that exists, use it directly
      files = [pattern];
    } else if (pattern.includes('*') || pattern.includes('?') || pattern.includes('[')) {
      // Treat as glob pattern if it contains glob characters
      files = await glob(pattern);
      if (files.length === 0) {
        allResults.push({
          file: pattern,
          line: null,
          message: `No files match pattern: ${pattern}`,
          severity: 'warning',
        });
      }
    } else {
      // Literal file path that doesn't exist
      allResults.push({
        file: pattern,
        line: null,
        message: `File not found: ${pattern}`,
        severity: 'error',
      });
      continue;
    }

    for (const file of files) {
      const results = lintFile(file);
      allResults.push(...results);
    }
  }

  // Report results
  for (const result of allResults) {
    const location = result.line ? `${result.file}:${result.line}` : result.file;
    const severity = result.severity.toUpperCase().padEnd(7);

    if (result.severity === 'error') {
      errorCount++;
      console.error(`${severity} ${location}: ${result.message}`);
    } else {
      warningCount++;
      console.warn(`${severity} ${location}: ${result.message}`);
    }
  }

  // Summary
  if (errorCount > 0 || warningCount > 0) {
    console.log(`\nFound ${errorCount} error(s) and ${warningCount} warning(s)`);
  } else {
    console.log('No issues found');
  }

  // Exit with error code if there were errors
  process.exit(errorCount > 0 ? 1 : 0);
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
