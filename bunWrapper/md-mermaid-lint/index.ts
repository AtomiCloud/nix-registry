#!/usr/bin/env bun
/**
 * md-mermaid-lint: Lint markdown files containing mermaid diagrams
 *
 * This tool validates markdown files and validates mermaid diagram syntax.
 * It uses mermaid.parse() for supported diagrams and falls back to
 * syntax-based validation for diagrams that require browser environment.
 */

import { glob } from 'glob';
import { readFileSync, existsSync, statSync } from 'fs';
import { join } from 'path';
import { unified } from 'unified';
import remarkParse from 'remark-parse';
import { visit } from 'unist-util-visit';

const VERSION = '0.1.0';

interface LintResult {
  file: string;
  line: number;
  message: string;
  severity: 'error' | 'warning' | 'success';
  diagramType?: string;
}

interface CodeNode {
  type: 'code';
  lang?: string | null;
  value?: string;
  position?: {
    start?: { line?: number };
    end?: { line?: number };
  };
}

// Track if mermaid has been initialized
let mermaidInitialized = false;
let mermaidModule: typeof import('mermaid').default | null = null;

// Supported diagram types that work in CLI
const CLI_SUPPORTED_DIAGRAMS = ['graph', 'flowchart', 'sequenceDiagram', 'gantt', 'classDiagram', 'gitGraph'];

/**
 * Check if a line starts with a specific diagram type using word-boundary matching
 * This prevents false positives like 'stateful nonsense' matching 'state'
 */
function startsWithDiagramType(line: string, type: string): boolean {
  return line === type || line.startsWith(type + ' ') || line.startsWith(type + '-');
}

/**
 * Check if diagram type is supported in CLI environment
 */
function isCLISupportedDiagram(code: string): boolean {
  const firstLine = code.split('\n')[0].trim().toLowerCase();
  return CLI_SUPPORTED_DIAGRAMS.some(type => startsWithDiagramType(firstLine, type.toLowerCase()));
}

/**
 * Basic syntax validation for mermaid diagrams
 * This validates diagram structure without rendering
 */
function validateSyntax(code: string): { valid: boolean; error?: string } {
  const lines = code.trim().split('\n');

  if (lines.length === 0) {
    return { valid: false, error: 'Empty mermaid diagram' };
  }

  const firstLine = lines[0].trim().toLowerCase();

  // Check for valid diagram type declaration
  const validDiagramTypes = [
    'graph',
    'flowchart',
    'sequencediagram',
    'statediagram',
    'statediagram-v2',
    'classdiagram',
    'gantt',
    'pie',
    'erdiagram',
    'journey',
    'gitgraph',
    'mindmap',
    'timeline',
    'quadrantchart',
    'requirementdiagram',
    'c4context',
    'blockdiag',
    'sequence',
    'state',
    'class',
    'er',
    'journey',
    'pie',
  ];

  const hasValidType = validDiagramTypes.some(type => startsWithDiagramType(firstLine, type));

  if (!hasValidType) {
    // Check if it might be a config line
    if (firstLine.startsWith('%%')) {
      // Comment line, check next line
      const nextLine = lines[1]?.trim().toLowerCase() || '';
      const hasNextValidType = validDiagramTypes.some(type => startsWithDiagramType(nextLine, type));
      if (!hasNextValidType) {
        return { valid: false, error: 'No valid mermaid diagram type detected' };
      }
    } else {
      // All mermaid diagrams must start with a diagram type declaration
      return { valid: false, error: 'No valid mermaid diagram type detected' };
    }
  }

  // Check for balanced brackets/braces
  const bracketStack: string[] = [];
  const bracketPairs: Record<string, string> = { '{': '}', '[': ']', '(': ')' };
  const closingBrackets = new Set(['}', ']', ')']);

  for (const line of lines) {
    // Skip comment lines
    if (line.trim().startsWith('%%')) continue;

    for (const char of line) {
      if (char in bracketPairs) {
        bracketStack.push(char);
      } else if (closingBrackets.has(char)) {
        // Check if stack is empty first (unmatched closing bracket)
        if (bracketStack.length === 0) {
          return { valid: false, error: `Unmatched closing bracket: ${char}` };
        }
        const lastOpen = bracketStack.pop();
        if (lastOpen && bracketPairs[lastOpen] !== char) {
          return { valid: false, error: `Unbalanced brackets: expected ${bracketPairs[lastOpen]} but found ${char}` };
        }
      }
    }
  }

  if (bracketStack.length > 0) {
    return { valid: false, error: 'Unbalanced brackets in diagram' };
  }

  // Diagram type-specific validation
  if (startsWithDiagramType(firstLine, 'graph') || startsWithDiagramType(firstLine, 'flowchart')) {
    // Validate flowchart syntax
    const hasNodes = lines.some(
      line =>
        /[A-Za-z0-9]+[\[\(\{]/.test(line) || // Node with shape
        /-->/.test(line) || // Connection
        /---/.test(line), // Link
    );
    if (!hasNodes && lines.length < 2) {
      return { valid: false, error: 'Flowchart has no valid nodes or connections' };
    }
  } else if (startsWithDiagramType(firstLine, 'sequencediagram') || firstLine === 'sequence') {
    // Validate sequence diagram has participants
    const hasParticipant = lines.some(
      line => /participant\s+\w+/.test(line) || /\w+->>?\w+/.test(line) || /\w+-->>\w+/.test(line),
    );
    if (!hasParticipant) {
      return { valid: false, error: 'Sequence diagram has no participants or messages' };
    }
  } else if (startsWithDiagramType(firstLine, 'statediagram')) {
    // Validate state diagram has transitions
    const hasTransition = lines.some(line => /-->/.test(line) && !line.trim().startsWith('%%'));
    if (!hasTransition) {
      return { valid: false, error: 'State diagram has no valid transitions' };
    }
    // Check for incomplete transitions (arrows without target)
    const incompleteTransition = lines.some(line => {
      const trimmed = line.trim();
      // Check for pattern like "[*] -->" (arrow at end of line without target)
      if (/-->\s*$/.test(trimmed)) {
        return true;
      }
      return false;
    });
    if (incompleteTransition) {
      return { valid: false, error: 'State diagram has incomplete transition (arrow without target)' };
    }
  } else if (startsWithDiagramType(firstLine, 'journey')) {
    // Journey diagrams need title and tasks
    const hasTitle = lines.some(line => /^\s*title\s+/.test(line.trim()));
    const hasTask = lines.some(line => /^\s*task\s+/.test(line.trim()));
    const hasSection = lines.some(line => /^\s*section\s+/.test(line.trim()));

    if (!hasTitle && !hasTask && !hasSection && lines.length < 3) {
      return { valid: false, error: 'Journey diagram has no valid title, section, or tasks' };
    }
    // Check if it just has "nonsense" content
    const validContent = lines.some(line => {
      const trimmed = line.trim().toLowerCase();
      return (
        trimmed.startsWith('title') ||
        trimmed.startsWith('section') ||
        trimmed.startsWith('task') ||
        /,\s*\d+:\s*\d+/.test(trimmed)
      );
    });
    if (!validContent) {
      return { valid: false, error: 'Journey diagram has no valid structure (title/section/task required)' };
    }
  }

  return { valid: true };
}

/**
 * Initialize mermaid for parsing
 */
async function initMermaid(): Promise<void> {
  if (!mermaidInitialized) {
    // Suppress mermaid's internal logging
    const originalError = console.error;
    const originalWarn = console.warn;
    console.error = () => {};
    console.warn = () => {};

    try {
      // Dynamically import mermaid
      const mermaidImport = await import('mermaid');
      mermaidModule = mermaidImport.default;

      // Initialize mermaid with minimal config for CLI usage
      mermaidModule.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        logLevel: 'fatal',
        quiet: true,
      });
      mermaidInitialized = true;
    } finally {
      // Restore console
      console.error = originalError;
      console.warn = originalWarn;
    }
  }
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

DESCRIPTION:
    Validates mermaid diagram syntax in markdown files. Supports
    both 'mermaid' and 'mmd' code block languages.

EXAMPLES:
    md-mermaid-lint README.md
    md-mermaid-lint docs/**/*.md
    md-mermaid-lint .

EXIT CODES:
    0 - All mermaid diagrams are valid
    1 - One or more errors found
`);
}

/**
 * Validate mermaid diagram syntax using mermaid.parse() when possible
 */
async function validateMermaid(code: string): Promise<{ valid: boolean; error?: string }> {
  // First do basic syntax validation
  const syntaxResult = validateSyntax(code);
  if (!syntaxResult.valid) {
    return syntaxResult;
  }

  // Try mermaid.parse() for supported diagrams
  if (mermaidModule && isCLISupportedDiagram(code)) {
    const originalError = console.error;
    const originalWarn = console.warn;
    console.error = () => {};
    console.warn = () => {};

    try {
      await mermaidModule.parse(code);
      return { valid: true };
    } catch (err) {
      const errorMessage = err instanceof Error ? err.message : String(err);

      // Handle specific errors
      if (errorMessage.includes('UnknownDiagramError') || errorMessage.includes('No diagram type detected')) {
        return { valid: false, error: 'No valid mermaid diagram type detected' };
      } else if (errorMessage.includes('Syntax error')) {
        const match = errorMessage.match(/Syntax error in text: (.+?)(?:\n|$)/);
        if (match) {
          return { valid: false, error: `Syntax error: ${match[1]}` };
        }
        return { valid: false, error: 'Syntax error in diagram' };
      } else if (errorMessage.includes('DOMPurify')) {
        // DOMPurify errors - fall back to syntax validation (already passed)
        return { valid: true };
      }

      return { valid: false, error: errorMessage };
    } finally {
      console.error = originalError;
      console.warn = originalWarn;
    }
  }

  // For unsupported diagrams, we already passed syntax validation
  return { valid: true };
}

/**
 * Lint a single markdown file for mermaid diagram issues
 */
async function lintFile(filePath: string): Promise<LintResult[]> {
  const results: LintResult[] = [];

  try {
    if (!existsSync(filePath)) {
      results.push({
        file: filePath,
        line: 0,
        message: `File not found: ${filePath}`,
        severity: 'error',
      });
      return results;
    }

    const stats = statSync(filePath);
    if (!stats.isFile()) {
      results.push({
        file: filePath,
        line: 0,
        message: `Not a file: ${filePath}`,
        severity: 'warning',
      });
      return results;
    }

    const content = readFileSync(filePath, 'utf-8');

    // Parse markdown using remark-parse
    const tree = unified().use(remarkParse).parse(content);

    // Find all mermaid code blocks
    const mermaidBlocks: { code: string; line: number }[] = [];

    visit(tree, 'code', (node: CodeNode) => {
      const lang = node.lang?.toLowerCase();
      if (lang === 'mermaid' || lang === 'mmd') {
        const line = node.position?.start?.line ?? 1;
        const code = node.value ?? '';
        mermaidBlocks.push({ code, line });
      }
    });

    // If no mermaid blocks found, return empty results
    if (mermaidBlocks.length === 0) {
      return results;
    }

    // Initialize mermaid if not already done
    await initMermaid();

    // Validate each mermaid block
    for (const { code, line } of mermaidBlocks) {
      // Check for empty diagram
      if (!code.trim()) {
        results.push({
          file: filePath,
          line,
          message: 'Empty mermaid diagram',
          severity: 'error',
        });
        continue;
      }

      // Validate mermaid syntax
      const validation = await validateMermaid(code);

      if (validation.valid) {
        results.push({
          file: filePath,
          line,
          message: 'Valid mermaid diagram',
          severity: 'success',
        });
      } else {
        results.push({
          file: filePath,
          line,
          message: validation.error ?? 'Invalid mermaid syntax',
          severity: 'error',
        });
      }
    }

    return results;
  } catch (err) {
    results.push({
      file: filePath,
      line: 0,
      message: `Failed to lint file: ${err instanceof Error ? err.message : String(err)}`,
      severity: 'error',
    });
    return results;
  }
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
  let successCount = 0;

  for (const pattern of args.files) {
    let files: string[];
    const stats = existsSync(pattern) && statSync(pattern);

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
          line: 0,
          message: `No files match pattern: ${pattern}`,
          severity: 'warning',
        });
      }
    } else {
      // Literal file path that doesn't exist
      allResults.push({
        file: pattern,
        line: 0,
        message: `File not found: ${pattern}`,
        severity: 'error',
      });
      continue;
    }

    for (const file of files) {
      const results = await lintFile(file);
      allResults.push(...results);
    }
  }

  // Report results
  for (const result of allResults) {
    const location = `${result.file}:${result.line}`;

    if (result.severity === 'error') {
      errorCount++;
      console.error(`ERROR ${location}: ${result.message}`);
    } else if (result.severity === 'warning') {
      warningCount++;
      console.warn(`WARNING ${location}: ${result.message}`);
    } else if (result.severity === 'success') {
      successCount++;
      console.log(`\u2705 ${location} - ${result.message}`);
    }
  }

  // Summary
  if (errorCount > 0 || warningCount > 0) {
    console.log(`\nFound ${errorCount} error(s) and ${warningCount} warning(s)`);
    if (successCount > 0) {
      console.log(`${successCount} valid diagram(s)`);
    }
  } else if (successCount > 0) {
    // All diagrams valid - no need for additional summary
  } else {
    console.log('No mermaid diagrams found');
  }

  // Exit with error code if there were errors
  process.exit(errorCount > 0 ? 1 : 0);
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
