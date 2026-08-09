// @bun
// cyan/src/loss-guard.ts
function spacesLike(value) {
  return value.replace(/[^\n]/g, " ");
}
function maskNixTrivia(source) {
  let output = "";
  let index = 0;
  while (index < source.length) {
    if (source[index] === "#") {
      const end = source.indexOf(`
`, index);
      const stop = end === -1 ? source.length : end;
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith("/*", index)) {
      const close = source.indexOf("*/", index + 2);
      const stop = close === -1 ? source.length : close + 2;
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source[index] === '"') {
      let stop = index + 1;
      while (stop < source.length) {
        if (source[stop] === "\\") {
          stop += 2;
          continue;
        }
        stop++;
        if (source[stop - 1] === '"')
          break;
      }
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith("''", index)) {
      let scan = index + 2;
      while (scan < source.length) {
        if (source.startsWith("'''", scan) || source.startsWith("''${", scan)) {
          scan += 3;
          continue;
        }
        if (source.startsWith("''\\", scan)) {
          scan += 4;
          continue;
        }
        if (source.startsWith("''", scan)) {
          scan += 2;
          break;
        }
        scan++;
      }
      const stop = Math.min(scan, source.length);
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    output += source[index];
    index++;
  }
  return output;
}
function splitTopLevel(value) {
  const parts = [];
  let start = 0;
  let braces = 0;
  let brackets = 0;
  let parentheses = 0;
  for (let index = 0;index < value.length; index++) {
    switch (value[index]) {
      case "{":
        braces++;
        break;
      case "}":
        braces--;
        break;
      case "[":
        brackets++;
        break;
      case "]":
        brackets--;
        break;
      case "(":
        parentheses++;
        break;
      case ")":
        parentheses--;
        break;
      case ",":
        if (braces === 0 && brackets === 0 && parentheses === 0) {
          parts.push(value.slice(start, index));
          start = index + 1;
        }
        break;
    }
  }
  parts.push(value.slice(start));
  return parts;
}
function findFunctionHeader(source) {
  const code = maskNixTrivia(source);
  const open = code.search(/\S/);
  if (open === -1 || code[open] !== "{")
    return null;
  let depth = 0;
  let close = -1;
  for (let index = open;index < code.length; index++) {
    if (code[index] === "{")
      depth++;
    if (code[index] === "}") {
      depth--;
      if (depth === 0) {
        close = index;
        break;
      }
    }
  }
  if (close === -1)
    return null;
  let colon = close + 1;
  while (colon < code.length && /\s/.test(code[colon]))
    colon++;
  if (code[colon] !== ":")
    return null;
  const bodyStart = open + 1;
  const masked = code.slice(bodyStart, close);
  const args = [];
  const argSources = new Map;
  let hasEllipsis = false;
  let offset = 0;
  for (const maskedPart of splitTopLevel(masked)) {
    const start = offset;
    offset += maskedPart.length + 1;
    const trimmed = maskedPart.trim();
    if (trimmed === "...") {
      hasEllipsis = true;
      continue;
    }
    if (trimmed.length === 0)
      continue;
    const name = trimmed.match(/^([a-zA-Z_][a-zA-Z0-9_'-]*)\b/)?.[1];
    if (name === undefined)
      continue;
    const leading = maskedPart.length - maskedPart.trimStart().length;
    const from = bodyStart + start + leading;
    if (!argSources.has(name))
      args.push(name);
    argSources.set(name, source.slice(from, from + trimmed.length));
  }
  return {
    args,
    argSources,
    colonIndex: colon,
    openIndex: open,
    closeIndex: close,
    hasEllipsis
  };
}
function withoutLeadingInheritSource(value) {
  const trimmed = value.trimStart();
  if (!trimmed.startsWith("("))
    return trimmed;
  let depth = 0;
  for (let index = 0;index < trimmed.length; index++) {
    if (trimmed[index] === "(")
      depth++;
    if (trimmed[index] === ")") {
      depth--;
      if (depth === 0)
        return trimmed.slice(index + 1);
    }
  }
  return trimmed;
}
function inventoryMaterial(source) {
  const code = maskNixTrivia(source);
  const header = findFunctionHeader(source);
  const args = new Set(header?.args ?? []);
  const bindings = new Set;
  const inherited = new Set;
  const withPreludes = new Set;
  for (const match of code.matchAll(/(?:^|[\n;{])\s*([a-zA-Z_][a-zA-Z0-9_'-]*(?:\.[a-zA-Z_][a-zA-Z0-9_'-]*)*)\s*=/gm)) {
    for (const segment of match[1].split("."))
      bindings.add(segment);
  }
  for (const match of code.matchAll(/\binherit\b([^;]*);/g)) {
    const body = withoutLeadingInheritSource(match[1]);
    for (const identifier of body.match(/[a-zA-Z_][a-zA-Z0-9_'-]*/g) ?? []) {
      inherited.add(identifier);
    }
  }
  for (const match of code.matchAll(/\bwith\s+([a-zA-Z_][a-zA-Z0-9_'-]*(?:\s*\.\s*[a-zA-Z_][a-zA-Z0-9_'-]*)*)\s*;/g)) {
    withPreludes.add(match[1].replace(/\s+/g, ""));
  }
  return { args, bindings, inherited, withPreludes };
}
function describe(unit) {
  switch (unit.kind) {
    case "arg":
      return `function argument '${unit.value}'`;
    case "binding":
      return `binding '${unit.value}'`;
    case "inherit":
      return `inherited identifier '${unit.value}'`;
    case "with":
      return `prelude 'with ${unit.value};'`;
  }
}
function lostUnits(inputs, output) {
  const actual = inventoryMaterial(output);
  const lost = [];
  const seen = new Set;
  const check = (kind, value, present) => {
    if (present.has(value))
      return;
    const key = `${kind}:${value}`;
    if (seen.has(key))
      return;
    seen.add(key);
    lost.push({ kind, value });
  };
  for (const input of inputs) {
    const expected = inventoryMaterial(input);
    for (const value of expected.args)
      check("arg", value, actual.args);
    for (const value of expected.withPreludes)
      check("with", value, actual.withPreludes);
    for (const value of expected.inherited) {
      if (actual.inherited.has(value) || actual.bindings.has(value))
        continue;
      check("inherit", value, actual.inherited);
    }
    for (const value of expected.bindings) {
      if (actual.bindings.has(value) || actual.inherited.has(value))
        continue;
      check("binding", value, actual.bindings);
    }
  }
  lost.sort((a, b) => a.kind === b.kind ? a.value.localeCompare(b.value) : a.kind.localeCompare(b.kind));
  return lost;
}
function assertNoLoss(file, inputs, output) {
  const lost = lostUnits(inputs, output);
  if (lost.length === 0)
    return;
  const shown = lost.slice(0, 24).map(describe).join(", ");
  const remainder = lost.length > 24 ? `, plus ${lost.length - 24} more` : "";
  throw new Error(`Cannot merge ${file}: the merge lost ${shown}${remainder}. ` + `Every function argument, 'with' prelude, inherited identifier and binding present in an ` + `input must survive into the merged output; refusing rather than emitting a file that is ` + `missing them. This usually means the input uses a shape the ${file} merger does not model.`);
}
function withLossGuard(file, merge) {
  return (sortedFiles) => {
    const output = merge(sortedFiles);
    assertNoLoss(file, sortedFiles.map((f) => f.content), output);
    return output;
  };
}

// cyan/src/merge-flake.ts
function findMatchingBrace(text, openIdx) {
  let depth = 1;
  let i = openIdx;
  while (i < text.length && depth > 0) {
    if (text[i] === "{")
      depth++;
    else if (text[i] === "}")
      depth--;
    if (depth === 0)
      return i;
    i++;
  }
  return -1;
}
function appendPendingComment(pending, line) {
  if (line || pending[pending.length - 1] !== "")
    pending.push(line);
}
function commentLabel(pending) {
  let first = 0;
  let last = pending.length;
  while (first < last && pending[first] === "")
    first++;
  while (last > first && pending[last - 1] === "")
    last--;
  return pending.slice(first, last).join(`
`);
}
function pushCommentBlock(lines, label, indentation) {
  for (const commentLine of label.split(`
`)) {
    lines.push(commentLine ? `${indentation}${commentLine}` : "");
  }
}
function parseInputsBlock(content) {
  const inputsMatch = content.match(/inputs\s*=\s*\{/);
  if (!inputsMatch)
    return [];
  const braceStart = inputsMatch.index + inputsMatch[0].length;
  const closingIdx = findMatchingBrace(content, braceStart);
  if (closingIdx === -1)
    return [];
  const body = content.slice(braceStart, closingIdx);
  const lines = body.split(`
`);
  const groups = [];
  let currentGroup = null;
  let pendingComments = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      if (pendingComments.length > 0)
        appendPendingComment(pendingComments, "");
      continue;
    }
    if (trimmed.startsWith("#")) {
      if (currentGroup?.items.length)
        currentGroup = null;
      appendPendingComment(pendingComments, trimmed);
      continue;
    }
    const entryMatch = trimmed.match(/^([\w-]+)\.url\s*=\s*"([^"]+)"\s*;?\s*(#.*)?$/);
    if (entryMatch) {
      const trailingComment = entryMatch[3] ? ` ${entryMatch[3]}` : "";
      const entry = `${entryMatch[1]}.url = "${entryMatch[2]}";${trailingComment}`;
      if (!currentGroup || pendingComments.length > 0) {
        currentGroup = { label: commentLabel(pendingComments), items: [] };
        groups.push(currentGroup);
        pendingComments = [];
      }
      currentGroup.items.push(entry);
    }
  }
  return groups;
}
function parseInputEntries(groups) {
  const entries = [];
  for (const group of groups) {
    for (const item of group.items) {
      const m = item.match(/^([\w-]+)\.url\s*=\s*"([^"]+)"\s*;?\s*(#.*)?$/);
      if (m)
        entries.push({ name: m[1], url: m[2], trailingComment: m[3] ?? "" });
    }
  }
  return entries;
}
function parseOutputBinding(content) {
  const match = content.match(/outputs\s*=\s*(?:([a-zA-Z_][\w'-]*)\s*@\s*)?\{/);
  if (!match)
    return {
      groups: [],
      expressions: new Map,
      optional: new Set,
      alias: null
    };
  const braceStart = match.index + match[0].length;
  let depth = 1;
  let i = braceStart;
  while (i < content.length && depth > 0) {
    if (content[i] === "{")
      depth++;
    else if (content[i] === "}")
      depth--;
    if (depth === 0)
      break;
    i++;
  }
  if (depth !== 0)
    return {
      groups: [],
      expressions: new Map,
      optional: new Set,
      alias: null
    };
  const body = content.slice(braceStart, i);
  const suffix = content.slice(i + 1);
  const aliasMatch = suffix.match(/^\s*@\s*([a-zA-Z_][\w'-]*)\s*:/);
  const lines = body.split(`
`);
  const groups = [];
  const expressions = new Map;
  const optional = new Set;
  let currentGroup = null;
  let pendingComments = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      if (pendingComments.length > 0)
        appendPendingComment(pendingComments, "");
      continue;
    }
    if (trimmed.startsWith("#")) {
      if (currentGroup?.items.length)
        currentGroup = null;
      appendPendingComment(pendingComments, trimmed);
      continue;
    }
    const formals = trimmed.replace(/\s+#.*$/, "").split(",").map((part) => part.trim()).flatMap((expression) => {
      if (expression === "...")
        return [{ name: "...", expression, optional: false }];
      const formal = expression.match(/^([a-zA-Z][\w-]*)(\s*\?.+)?$/);
      if (!formal)
        return [];
      return [
        {
          name: formal[1],
          expression,
          optional: formal[2] !== undefined
        }
      ];
    });
    if (formals.length > 0) {
      if (!currentGroup || pendingComments.length > 0) {
        currentGroup = { label: commentLabel(pendingComments), items: [] };
        groups.push(currentGroup);
        pendingComments = [];
      }
      for (const formal of formals) {
        currentGroup.items.push(formal.name);
        expressions.set(formal.name, formal.expression);
        if (formal.optional)
          optional.add(formal.name);
        else
          optional.delete(formal.name);
      }
    }
  }
  return {
    groups,
    expressions,
    optional,
    alias: match[1] ?? aliasMatch?.[1] ?? null
  };
}
function parseInputPreambleComments(content) {
  const inputsMatch = content.match(/inputs\s*=/);
  if (!inputsMatch)
    return [];
  return content.slice(0, inputsMatch.index).split(`
`).map((line) => line.trim()).filter((line) => line.startsWith("#"));
}
function parseRegistryLines(content) {
  const systemMatch = content.match(/system:\s*\n/);
  if (!systemMatch)
    return [];
  const afterSystem = content.slice(systemMatch.index + systemMatch[0].length);
  const letMatch = afterSystem.match(/\blet\b/);
  if (!letMatch)
    return [];
  const afterLet = afterSystem.slice(letMatch.index + 3);
  const lines = afterLet.split(`
`);
  const registryLines = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed)
      continue;
    if (trimmed === "in" || trimmed.startsWith("in "))
      break;
    const m = trimmed.match(/^([\w-]+)\s*=\s*(.+);\s*$/);
    if (m) {
      registryLines.push({ name: m[1], expr: m[2] });
    }
  }
  return registryLines;
}
function parsePkgsAlias(content) {
  const systemMatch = content.match(/system\s*:/);
  const withRecMatch = content.match(/with\s+rec\s*\{/);
  if (!systemMatch || !withRecMatch || withRecMatch.index <= systemMatch.index)
    return null;
  const body = content.slice(systemMatch.index, withRecMatch.index);
  const matches = [...body.matchAll(/\b(pkgs\s*=\s*[^;]+;)/g)];
  return matches.length > 0 ? matches[matches.length - 1][1].trim() : null;
}
function lineIndentAt(text, offset) {
  const lineStart = text.lastIndexOf(`
`, offset - 1) + 1;
  const prefix = text.slice(lineStart, offset);
  return /^[ \t]*$/.test(prefix) ? prefix : "";
}
function commentsAbove(text, offset) {
  let lineStart = text.lastIndexOf(`
`, offset - 1) + 1;
  if (!/^[ \t]*$/.test(text.slice(lineStart, offset)))
    return [];
  const comments = [];
  while (lineStart > 0) {
    const previousEnd = lineStart - 1;
    const previousStart = previousEnd === 0 ? 0 : text.lastIndexOf(`
`, previousEnd - 1) + 1;
    const line = text.slice(previousStart, previousEnd).trim();
    if (!line.startsWith("#"))
      break;
    comments.unshift(line);
    lineStart = previousStart;
  }
  return comments;
}
function findWithRecBody(content, code) {
  const match = code.match(/with\s+rec\s*\{/);
  if (!match)
    return null;
  const braceStart = match.index + match[0].length;
  const closingIdx = findMatchingBrace(code, braceStart);
  return closingIdx === -1 ? null : { braceStart, closingIdx };
}
function parseWithRecAssignments(content) {
  const code = maskNixTrivia(content);
  const block = findWithRecBody(content, code);
  if (!block)
    return [];
  const { braceStart, closingIdx } = block;
  const body = content.slice(braceStart, closingIdx);
  const scan = code.slice(braceStart, closingIdx);
  const assignments = [];
  let nameStart = -1;
  let nameEnd = -1;
  let rhsStart = -1;
  let depth = 0;
  let inAssignment = false;
  const record = (end) => {
    assignments.push({
      name: body.slice(nameStart, nameEnd).replace(/[\s;]/g, ""),
      body: body.slice(rhsStart, end).trim().replace(/\s*;\s*$/, ""),
      raw: body.slice(nameStart, end).trimEnd(),
      indent: lineIndentAt(body, nameStart),
      leadingComments: commentsAbove(body, nameStart),
      start: braceStart + nameStart,
      end: braceStart + end
    });
  };
  let pos = 0;
  while (pos < scan.length) {
    const char = scan[pos];
    if (!inAssignment) {
      if (char === "=" && nameStart !== -1) {
        inAssignment = true;
        rhsStart = pos + 1;
        pos++;
        continue;
      }
      if (!/[\s;]/.test(char)) {
        if (nameStart === -1)
          nameStart = pos;
        nameEnd = pos + 1;
      }
      pos++;
    } else {
      if (char === "{")
        depth++;
      else if (char === "}") {
        depth--;
        if (depth < 0)
          break;
      }
      pos++;
      if (char === ";" && depth === 0) {
        record(pos);
        nameStart = -1;
        nameEnd = -1;
        rhsStart = -1;
        depth = 0;
        inAssignment = false;
      }
    }
  }
  if (nameStart !== -1 && rhsStart !== -1 && body.slice(rhsStart, pos).trim()) {
    record(pos);
  }
  return assignments;
}
function parseFinalInheritIds(content) {
  const inherit = findFinalInherit(content);
  return inherit ? inherit.ids : [];
}
function findFinalInherit(content) {
  const code = maskNixTrivia(content);
  const block = findWithRecBody(content, code);
  if (!block)
    return null;
  const afterRec = code.slice(block.closingIdx);
  const match = afterRec.match(/\{\s*inherit\s+([^;]+);\s*\}/);
  if (!match)
    return null;
  const idsText = match[1];
  return {
    ids: idsText.trim().split(/\s+/).filter(Boolean),
    idsText,
    semicolon: block.closingIdx + match.index + match[0].indexOf(";")
  };
}
function extractInheritIds(assignmentBody) {
  const inheritMatch = assignmentBody.match(/inherit\s+([^;]+);/);
  if (!inheritMatch)
    return [];
  return inheritMatch[1].trim().split(/\s+/);
}
function extractPackagesInheritIds(assignments) {
  const pkg = assignments.find((a) => a.name === "packages");
  if (!pkg)
    return [];
  return extractInheritIds(pkg.body);
}
function parseFlake(content) {
  const descriptionMatch = content.match(/description\s*=\s*"([^"]+)"/);
  const description = descriptionMatch ? descriptionMatch[1] : "";
  const inputPreambleComments = parseInputPreambleComments(content);
  const inputGroups = parseInputsBlock(content);
  const outputBinding = parseOutputBinding(content);
  const registryLines = parseRegistryLines(content);
  const pkgsAlias = parsePkgsAlias(content);
  const withRecAssignments = parseWithRecAssignments(content);
  const finalInheritIds = parseFinalInheritIds(content);
  return {
    description,
    inputPreambleComments,
    inputGroups,
    outputParamGroups: outputBinding.groups,
    outputParamExpressions: outputBinding.expressions,
    optionalOutputParams: outputBinding.optional,
    outputsAlias: outputBinding.alias,
    registryLines,
    pkgsAlias,
    withRecAssignments,
    finalInheritIds
  };
}
function findBracedRegion(content, pattern, scan = content) {
  const match = scan.match(pattern);
  if (!match)
    return null;
  const relativeOpen = match[0].lastIndexOf("{");
  const open = match.index + relativeOpen;
  const close = findMatchingBrace(scan, open + 1);
  return close === -1 ? null : { open, close };
}
function inputName(item) {
  return item.match(/^\s*([\w-]+)\.url\s*=/)?.[1] ?? null;
}
function insertBeforeClosingBrace(content, close, block) {
  const closingLineStart = content.lastIndexOf(`
`, close) + 1;
  const beforeBrace = content.slice(closingLineStart, close);
  if (/^\s*$/.test(beforeBrace)) {
    return content.slice(0, closingLineStart) + block + `
` + content.slice(closingLineStart);
  }
  const closingIndent = beforeBrace.match(/^\s*/)?.[0] ?? "";
  return content.slice(0, close) + `
` + block + `
` + closingIndent + content.slice(close);
}
function insertMissingInputs(content, mergedGroups) {
  const region = findBracedRegion(content, /inputs\s*=\s*\{/);
  if (!region)
    throw new Error("Cannot merge flake.nix: inputs attribute set was not found");
  const baseNames = new Set(parseInputEntries(parseInputsBlock(content)).map((entry) => entry.name));
  const body = content.slice(region.open + 1, region.close);
  const entryIndent = body.match(/^([ \t]*)[\w-]+\.url\s*=/m)?.[1] ?? "    ";
  const blocks = [];
  for (const group of mergedGroups) {
    const missing = group.items.filter((item) => {
      const name = inputName(item);
      return name !== null && !baseNames.has(name);
    });
    if (missing.length === 0)
      continue;
    const lines = [];
    if (group.label)
      pushCommentBlock(lines, group.label, entryIndent);
    for (const item of missing) {
      const name = inputName(item);
      baseNames.add(name);
      lines.push(entryIndent + item.trimStart());
    }
    blocks.push(lines.join(`
`));
  }
  return blocks.length === 0 ? content : insertBeforeClosingBrace(content, region.close, blocks.join(`

`));
}
function insertMissingOutputParams(content, mergedGroups, expressionByName) {
  const region = findBracedRegion(content, /outputs\s*=\s*(?:[a-zA-Z_][\w'-]*\s*@\s*)?\{/);
  if (!region)
    throw new Error("Cannot merge flake.nix: outputs argument set was not found");
  const baseBinding = parseOutputBinding(content);
  const baseNames = new Set(baseBinding.groups.flatMap((group) => group.items));
  const rawMissingGroups = mergedGroups.map((group) => ({
    label: group.label,
    items: group.items.filter((item) => !baseNames.has(item))
  })).filter((group) => group.items.length > 0);
  const ellipsisGroup = rawMissingGroups.find((group) => group.items.includes("..."));
  const missingGroups = rawMissingGroups.map((group) => ({
    label: group.label,
    items: group.items.filter((item) => item !== "...")
  })).filter((group) => group.items.length > 0);
  if (ellipsisGroup) {
    missingGroups.push({
      label: ellipsisGroup.items.length === 1 ? ellipsisGroup.label : "",
      items: ["..."]
    });
  }
  if (missingGroups.length === 0)
    return content;
  for (const group of missingGroups) {
    for (const item of group.items)
      baseNames.add(item);
  }
  const body = content.slice(region.open + 1, region.close);
  const missingNames = missingGroups.flatMap((group) => group.items);
  if (!body.includes(`
`)) {
    const ellipsisOffset = body.indexOf("...");
    if (ellipsisOffset >= 0) {
      const ordinary = missingNames.filter((name) => name !== "...");
      if (ordinary.length === 0)
        return content;
      const insertAt = region.open + 1 + ellipsisOffset;
      return content.slice(0, insertAt) + ordinary.map((name) => expressionByName.get(name) ?? name).join(", ") + ", " + content.slice(insertAt);
    }
    const trimmedBody = body.trimEnd();
    const separator = trimmedBody ? trimmedBody.endsWith(",") ? " " : ", " : "";
    const rendered = missingNames.map((name) => expressionByName.get(name) ?? name).join(", ");
    return content.slice(0, region.close) + separator + rendered + content.slice(region.close);
  }
  const usesLeadingCommas = /^\s*,/m.test(body) || !/[\w)-]\s*,\s*(?:#.*)?$/m.test(body);
  const ellipsisMatch = body.match(/^\s*(?:,\s*)?\.\.\.\s*,?\s*(?:#.*)?$/m);
  const beforeInsertion = ellipsisMatch ? body.slice(0, ellipsisMatch.index) : body;
  const precedingFormalHasTrailingComma = beforeInsertion.split(`
`).reverse().map((line) => line.replace(/\s+#.*$/, "").trim()).find((line) => line.length > 0)?.endsWith(",") ?? false;
  const renderWithLeadingCommas = usesLeadingCommas || !precedingFormalHasTrailingComma;
  const entryIndent = usesLeadingCommas ? body.match(/^([ \t]*),\s*(?:[a-zA-Z_]|\.\.\.)/m)?.[1] ?? "    " : body.match(/^([ \t]*)(?:[a-zA-Z_]|\.\.\.)/m)?.[1] ?? "      ";
  const commentIndent = usesLeadingCommas ? `${entryIndent}  ` : entryIndent;
  const blocks = [];
  for (const group of missingGroups) {
    const lines = [];
    if (group.label)
      pushCommentBlock(lines, group.label, commentIndent);
    for (const name of group.items) {
      const expression = expressionByName.get(name) ?? name;
      if (renderWithLeadingCommas)
        lines.push(`${entryIndent}, ${expression}`);
      else if (name === "...")
        lines.push(`${entryIndent}...`);
      else
        lines.push(`${entryIndent}${expression},`);
    }
    blocks.push(lines.join(`
`));
  }
  const block = blocks.join(`

`);
  if (ellipsisMatch) {
    const insertAt = region.open + 1 + ellipsisMatch.index;
    return content.slice(0, insertAt) + block + `
` + content.slice(insertAt);
  }
  return insertBeforeClosingBrace(content, region.close, block);
}
function findSystemLetInsertion(content) {
  const systemMatch = content.match(/system\s*:/);
  if (!systemMatch)
    return null;
  const afterSystem = content.slice(systemMatch.index + systemMatch[0].length);
  const letMatch = afterSystem.match(/\blet\b/);
  if (!letMatch)
    return null;
  const afterLetStart = systemMatch.index + systemMatch[0].length + letMatch.index + letMatch[0].length;
  const afterLet = content.slice(afterLetStart);
  const inMatch = afterLet.match(/^([ \t]*)in(?:\s|$)/m);
  if (!inMatch)
    return null;
  const beforeIn = afterLet.slice(0, inMatch.index);
  const assignmentIndents = [...beforeIn.matchAll(/^([ \t]+)[\w-]+\s*=/gm)];
  const indent = assignmentIndents[0]?.[1] ?? `${inMatch[1]}  `;
  return { at: afterLetStart + inMatch.index, indent };
}
function insertMissingRegistryLines(content, mergedLines, mergedPkgsAlias) {
  const baseNames = new Set(parseRegistryLines(content).map((line) => line.name));
  const additions = mergedLines.filter((line) => !baseNames.has(line.name)).map((line) => `${line.name} = ${line.expr};`);
  if (mergedPkgsAlias && !parsePkgsAlias(content))
    additions.push(mergedPkgsAlias);
  if (additions.length === 0)
    return content;
  const insertion = findSystemLetInsertion(content);
  if (!insertion) {
    throw new Error("Cannot merge flake.nix: system let block was not found");
  }
  const block = additions.map((line) => insertion.indent + line).join(`
`);
  return content.slice(0, insertion.at) + block + `
` + content.slice(insertion.at);
}
function insertMissingPackageInherits(content, mergedIds) {
  if (mergedIds.length === 0)
    return content;
  const code = maskNixTrivia(content);
  const withRecRegion = findBracedRegion(content, /with\s+rec\s*\{/, code);
  if (!withRecRegion)
    throw new Error("Cannot merge flake.nix: with rec block was not found");
  const withRecBody = code.slice(withRecRegion.open + 1, withRecRegion.close);
  const packagesMatch = withRecBody.match(/\bpackages\s*=\s*import\b/);
  if (!packagesMatch)
    return content;
  const packagesStart = withRecRegion.open + 1 + packagesMatch.index;
  const argsOpen = code.indexOf("{", packagesStart + packagesMatch[0].length);
  if (argsOpen === -1 || argsOpen >= withRecRegion.close) {
    throw new Error("Cannot merge flake.nix: packages import argument set was not found");
  }
  const argsClose = findMatchingBrace(code, argsOpen + 1);
  if (argsClose === -1 || argsClose > withRecRegion.close) {
    throw new Error("Cannot merge flake.nix: packages import argument set is unbalanced");
  }
  const argsBody = code.slice(argsOpen + 1, argsClose);
  const inheritMatch = argsBody.match(/\binherit\b([\s\S]*?);/);
  if (!inheritMatch)
    return content;
  const existingIds = new Set((inheritMatch[1].match(/[a-zA-Z_][\w'-]*/g) ?? []).filter((id) => id !== "inherit"));
  const missing = mergedIds.filter((id) => !existingIds.has(id)).sort();
  if (missing.length === 0)
    return content;
  const semicolon = argsOpen + 1 + inheritMatch.index + inheritMatch[0].lastIndexOf(";");
  return spliceInheritIds(content, semicolon, inheritMatch[1], missing);
}
function spliceInheritIds(content, semicolon, inheritText, missing) {
  const appendInline = () => content.slice(0, semicolon) + " " + missing.join(" ") + content.slice(semicolon);
  if (!inheritText.includes(`
`))
    return appendInline();
  const semicolonLineStart = content.lastIndexOf(`
`, semicolon) + 1;
  const beforeSemicolon = content.slice(semicolonLineStart, semicolon);
  if (!/^\s*$/.test(beforeSemicolon))
    return appendInline();
  const block = missing.map((id) => beforeSemicolon + id).join(`
`);
  return content.slice(0, semicolonLineStart) + block + `
` + content.slice(semicolonLineStart);
}
function quoteNames(names) {
  return names.map((name) => `'${name}'`).join(", ");
}
function shiftIndent(raw, delta) {
  if (delta === 0)
    return raw;
  return raw.split(`
`).map((line, index) => {
    if (index === 0 || line.trim() === "")
      return line;
    if (delta > 0)
      return " ".repeat(delta) + line;
    const leading = line.match(/^[ \t]*/)[0];
    return line.slice(Math.min(leading.length, -delta));
  }).join(`
`);
}
function renderWithRecAssignment(assignment, indent) {
  const lines = assignment.leadingComments.map((comment) => indent + comment);
  const raw = assignment.raw.endsWith(";") ? assignment.raw : `${assignment.raw};`;
  lines.push(indent + shiftIndent(raw, indent.length - assignment.indent.length));
  return lines.join(`
`);
}
function insertMissingWithRecAssignments(content, merged) {
  const baseNames = new Set(parseWithRecAssignments(content).map((assignment) => assignment.name));
  const missing = merged.filter((assignment) => !baseNames.has(assignment.name));
  if (missing.length === 0)
    return content;
  const code = maskNixTrivia(content);
  const region = findBracedRegion(content, /with\s+rec\s*\{/, code);
  if (!region)
    throw new Error(`Cannot merge flake.nix: with rec block was not found, so ` + `${quoteNames(missing.map((assignment) => assignment.name))} could not be spliced`);
  const scan = code.slice(region.open + 1, region.close);
  const entryIndent = scan.match(/^([ \t]*)[\w'-]+\s*=/m)?.[1] ?? "          ";
  const block = missing.map((assignment) => renderWithRecAssignment(assignment, entryIndent)).join(`
`);
  return insertBeforeClosingBrace(content, region.close, block);
}
function insertMissingFinalInherits(content, mergedIds) {
  const target = findFinalInherit(content);
  const baseIds = new Set(target?.ids ?? []);
  const missing = mergedIds.filter((id) => !baseIds.has(id)).sort();
  if (missing.length === 0)
    return content;
  if (!target)
    throw new Error(`Cannot merge flake.nix: the final inherit attribute set was not found, so ` + `${quoteNames(missing)} could not be spliced`);
  return spliceInheritIds(content, target.semicolon, target.idsText, missing);
}
function findTrailingBracedRegion(scan, start, end) {
  let depth = 0;
  let open = -1;
  let region = null;
  for (let index = start;index < end; index++) {
    if (scan[index] === "{") {
      if (depth === 0)
        open = index;
      depth++;
    } else if (scan[index] === "}") {
      depth--;
      if (depth === 0 && open !== -1)
        region = { open, close: index };
      if (depth < 0)
        break;
    }
  }
  return region;
}
function classifyArgEntry(segment, scan) {
  const trimmed = segment.trim();
  const shape = scan.trim();
  if (!trimmed || !shape)
    return null;
  const offset = segment.length - segment.trimStart().length;
  const indent = lineIndentAt(segment, offset);
  const inherit = shape.match(/^inherit\b([\s\S]*);$/);
  if (inherit) {
    const names = inherit[1].replace(/^\s*\([\s\S]*?\)/, "").match(/[a-zA-Z_][\w'-]*/g);
    return names ? { kind: "inherit", names, text: trimmed, indent } : null;
  }
  const binding = shape.match(/^([a-zA-Z_][\w'-]*)\s*=/);
  return binding ? { kind: "binding", names: [binding[1]], text: trimmed, indent } : null;
}
function parseArgEntries(content, code, region) {
  const body = content.slice(region.open + 1, region.close);
  const scan = code.slice(region.open + 1, region.close);
  const entries = [];
  let depth = 0;
  let start = 0;
  for (let index = 0;index < scan.length; index++) {
    const char = scan[index];
    if (char === "{" || char === "[" || char === "(")
      depth++;
    else if (char === "}" || char === "]" || char === ")")
      depth--;
    else if (char === ";" && depth === 0) {
      const entry = classifyArgEntry(body.slice(start, index + 1), scan.slice(start, index + 1));
      if (entry)
        entries.push(entry);
      start = index + 1;
    }
  }
  return entries;
}
function insertArgEntry(content, assignmentName, entry, lostName) {
  const target = parseWithRecAssignments(content).find((assignment) => assignment.name === assignmentName);
  if (!target)
    return content;
  const code = maskNixTrivia(content);
  const region = findTrailingBracedRegion(code, target.start, target.end);
  if (!region)
    return content;
  const scan = code.slice(region.open + 1, region.close);
  const entryIndent = scan.match(/^([ \t]*)\S/m)?.[1] ?? "";
  if (entry.kind === "inherit") {
    const inheritMatch = scan.match(/\binherit\b([\s\S]*?);/);
    if (!inheritMatch)
      return insertBeforeClosingBrace(content, region.close, `${entryIndent}inherit ${lostName};`);
    const semicolon = region.open + 1 + inheritMatch.index + inheritMatch[0].lastIndexOf(";");
    return spliceInheritIds(content, semicolon, inheritMatch[1], [lostName]);
  }
  const rendered = shiftIndent(entry.text, entryIndent.length - entry.indent.length);
  return insertBeforeClosingBrace(content, region.close, entryIndent + rendered);
}
function rescueLostArgEntries(content, parsed, sources) {
  const required = new Set;
  for (const source of sources) {
    const inventory = inventoryMaterial(source);
    for (const name of inventory.bindings)
      required.add(name);
    for (const name of inventory.inherited)
      required.add(name);
  }
  const candidates = new Map;
  for (const [index, flake] of parsed.entries()) {
    const source = sources[index];
    const code = maskNixTrivia(source);
    for (const assignment of flake.withRecAssignments) {
      const region = findTrailingBracedRegion(code, assignment.start, assignment.end);
      if (!region)
        continue;
      for (const entry of parseArgEntries(source, code, region)) {
        for (const name of entry.names) {
          candidates.set(name, { assignmentName: assignment.name, entry });
        }
      }
    }
  }
  for (;; ) {
    const actual = inventoryMaterial(content);
    const lost = [...required].find((name) => !actual.bindings.has(name) && !actual.inherited.has(name) && candidates.has(name));
    if (lost === undefined)
      return content;
    const candidate = candidates.get(lost);
    candidates.delete(lost);
    content = insertArgEntry(content, candidate.assignmentName, candidate.entry, lost);
  }
}
function assertFinalInheritsAreBound(content, requiredIds) {
  if (requiredIds.length === 0)
    return;
  const bound = new Set;
  for (const assignment of parseWithRecAssignments(content))
    bound.add(assignment.name);
  for (const line of parseRegistryLines(content))
    bound.add(line.name);
  const alias = parsePkgsAlias(content);
  if (alias)
    bound.add(alias.split("=")[0].trim());
  for (const group of parseOutputBinding(content).groups)
    for (const item of group.items)
      bound.add(item);
  const unbound = requiredIds.filter((id) => !bound.has(id));
  if (unbound.length > 0)
    throw new Error(`Cannot merge flake.nix: final inherit ${quoteNames(unbound)} ` + `${unbound.length === 1 ? "is" : "are"} not bound by the merged with rec block`);
}
function assertMergeInvariants(content, mergedInputGroups, mergedOutputGroups) {
  const outputInputs = new Set(parseInputEntries(parseInputsBlock(content)).map((entry) => entry.name));
  const outputParams = new Set(parseOutputBinding(content).groups.flatMap((group) => group.items));
  const optionalOutputParams = parseOutputBinding(content).optional;
  const requiredInputs = new Set(mergedInputGroups.flatMap((group) => group.items.map(inputName).filter((name) => name !== null)));
  const requiredParams = new Set(mergedOutputGroups.flatMap((group) => group.items));
  for (const name of requiredInputs) {
    if (!outputInputs.has(name)) {
      throw new Error(`Cannot merge flake.nix: input '${name}' disappeared from the merged file`);
    }
  }
  for (const name of requiredParams) {
    if (!outputParams.has(name)) {
      throw new Error(`Cannot merge flake.nix: outputs argument '${name}' disappeared from the merged file`);
    }
  }
  if (!outputParams.has("...")) {
    for (const name of outputInputs) {
      if (!outputParams.has(name)) {
        throw new Error(`Cannot merge flake.nix: input '${name}' is not accepted by the outputs argument set`);
      }
    }
    for (const name of outputParams) {
      if (name !== "self" && !outputInputs.has(name) && !optionalOutputParams.has(name)) {
        throw new Error(`Cannot merge flake.nix: outputs argument '${name}' has no matching input`);
      }
    }
  }
}
function mergeFlake(sortedFiles) {
  if (sortedFiles.length === 0)
    throw new Error("Cannot merge flake.nix: no files were provided");
  const parsed = sortedFiles.map((f) => parseFlake(f.content));
  const mergedInputs = mergeInputEntries(parsed);
  const mergedOutputParams = mergeOutputParams(parsed);
  const mergedOutputExpressions = new Map;
  for (const flake of parsed) {
    for (const [name, expression] of flake.outputParamExpressions) {
      mergedOutputExpressions.set(name, expression);
    }
  }
  const mergedRegistries = mergeRegistryLines(parsed);
  const pkgsAliases = parsed.flatMap((p) => p.pkgsAlias ? [p.pkgsAlias] : []);
  const pkgsAlias = pkgsAliases[pkgsAliases.length - 1] ?? null;
  const allPackageInherits = new Set;
  for (const p of parsed) {
    for (const id of extractPackagesInheritIds(p.withRecAssignments)) {
      allPackageInherits.add(id);
    }
  }
  const mergedWithRec = mergeWithRecAssignments(parsed);
  const mergedFinalInherits = mergeFinalInheritIds(parsed);
  let content = sortedFiles[sortedFiles.length - 1].content;
  content = insertMissingInputs(content, mergedInputs);
  content = insertMissingOutputParams(content, mergedOutputParams, mergedOutputExpressions);
  content = insertMissingRegistryLines(content, mergedRegistries, pkgsAlias);
  content = insertMissingWithRecAssignments(content, mergedWithRec);
  content = insertMissingPackageInherits(content, [...allPackageInherits]);
  content = insertMissingFinalInherits(content, mergedFinalInherits);
  content = rescueLostArgEntries(content, parsed, sortedFiles.map((f) => f.content));
  assertFinalInheritsAreBound(content, mergedFinalInherits);
  assertMergeInvariants(content, mergedInputs, mergedOutputParams);
  return content;
}
function mergeWithRecAssignments(parsed) {
  const order = [];
  const byName = new Map;
  for (const p of parsed) {
    for (const assignment of p.withRecAssignments) {
      if (!byName.has(assignment.name))
        order.push(assignment.name);
      byName.set(assignment.name, assignment);
    }
  }
  return order.map((name) => byName.get(name));
}
function mergeFinalInheritIds(parsed) {
  const ids = new Set;
  for (const p of parsed) {
    for (const id of p.finalInheritIds)
      ids.add(id);
  }
  return [...ids];
}
function mergeInputEntries(parsed) {
  const entryByInput = new Map;
  const inputToGroup = new Map;
  for (const p of parsed) {
    const entries = parseInputEntries(p.inputGroups);
    for (const entry of entries) {
      entryByInput.set(entry.name, entry);
    }
    for (const group of p.inputGroups) {
      for (const item of group.items) {
        const name = item.match(/^([\w-]+)\./)?.[1];
        if (name) {
          inputToGroup.set(name, group.label);
        }
      }
    }
  }
  const groupMap = new Map;
  for (const [name, input] of entryByInput) {
    const group = inputToGroup.get(name) ?? "";
    const trailingComment = input.trailingComment ? ` ${input.trailingComment}` : "";
    const entry = `    ${name}.url = "${input.url}";${trailingComment}`;
    if (!groupMap.has(group))
      groupMap.set(group, []);
    groupMap.get(group).push(entry);
  }
  const groups = [];
  for (const [label, items] of groupMap) {
    items.sort((a, b) => a.localeCompare(b));
    groups.push({ label, items });
  }
  groups.sort((a, b) => {
    if (a.label === "" && b.label !== "")
      return -1;
    if (a.label !== "" && b.label === "")
      return 1;
    return a.label.localeCompare(b.label);
  });
  return groups;
}
function mergeOutputParams(parsed) {
  const paramToGroup = new Map;
  const allParams = new Set;
  for (const p of parsed) {
    for (const group of p.outputParamGroups) {
      for (const item of group.items) {
        allParams.add(item);
        paramToGroup.set(item, group.label);
      }
    }
  }
  const groupMap = new Map;
  for (const name of allParams) {
    const group = paramToGroup.get(name) ?? "";
    if (!groupMap.has(group))
      groupMap.set(group, []);
    groupMap.get(group).push(name);
  }
  const groups = [];
  for (const [label, items] of groupMap) {
    items.sort((a, b) => {
      if (a === "self")
        return -1;
      if (b === "self")
        return 1;
      if (a === "...")
        return 1;
      if (b === "...")
        return -1;
      return a.localeCompare(b);
    });
    groups.push({ label, items });
  }
  groups.sort((a, b) => {
    if (a.label === "" && b.label !== "")
      return -1;
    if (a.label !== "" && b.label === "")
      return 1;
    return a.label.localeCompare(b.label);
  });
  return groups;
}
function mergeRegistryLines(parsed) {
  const exprByName = new Map;
  const allNames = new Set;
  for (const p of parsed) {
    for (const line of p.registryLines) {
      allNames.add(line.name);
      exprByName.set(line.name, line.expr);
    }
  }
  return [...allNames].sort().map((name) => ({ name, expr: exprByName.get(name) }));
}

// cyan/src/merge-env.ts
function parseEnv(content) {
  const code = maskNixTrivia(content);
  const header = findFunctionHeader(content);
  if (!header) {
    throw new Error("Cannot merge nix/env.nix: no function argument set was found. The file must begin " + "with a `{ ... }:` head (single-line or multi-line); refusing rather than emitting " + "a headless skeleton.");
  }
  const renderedArgs = header.args.map((name) => (header.argSources.get(name) ?? name).replace(/\s+/g, " "));
  const functionArgs = `{ ${renderedArgs.join(", ")}${header.hasEllipsis ? ", ..." : ""} }`;
  const argSignature = JSON.stringify([...renderedArgs].sort().concat(header.hasEllipsis ? ["..."] : []));
  let cursor = header.colonIndex + 1;
  while (cursor < code.length && /\s/.test(code[cursor]))
    cursor++;
  const withMatch = code.slice(cursor).match(/^with\s+packages\s*;/);
  let withPackages = false;
  if (withMatch) {
    withPackages = true;
    cursor += withMatch[0].length;
  }
  if (code.slice(cursor).trimStart()[0] !== "{") {
    throw new Error("Cannot merge nix/env.nix: the top-level category attribute set was not found after " + "the function head. Refusing rather than emitting an empty skeleton.");
  }
  const categories = new Map;
  let currentCategory = null;
  let inList = false;
  const lines = content.split(`
`);
  const lineIdx = content.slice(0, cursor).split(`
`).length - 1;
  for (let i = lineIdx;i < lines.length; i++) {
    const line = lines[i];
    let trimmed = line.trim();
    if (trimmed === "{")
      continue;
    if (trimmed === "}")
      break;
    const catMatch = trimmed.match(/^([\w-]+)\s*=\s*\[/);
    if (catMatch) {
      currentCategory = catMatch[1];
      categories.set(currentCategory, []);
      inList = true;
      const rest = trimmed.slice(catMatch[0].length);
      const closing = rest.indexOf("]");
      if (closing !== -1) {
        for (const item of rest.slice(0, closing).trim().split(/\s+/).filter(Boolean)) {
          categories.get(currentCategory).push(item);
        }
        inList = false;
        currentCategory = null;
      }
      continue;
    }
    if (trimmed === "];") {
      inList = false;
      currentCategory = null;
      continue;
    }
    if (inList && currentCategory) {
      if (!trimmed || trimmed.startsWith("#"))
        continue;
      const commentIdx = trimmed.indexOf("#");
      if (commentIdx > 0 && trimmed[commentIdx - 1] === " ") {
        trimmed = trimmed.slice(0, commentIdx).trim();
      }
      if (trimmed)
        categories.get(currentCategory).push(trimmed);
    }
  }
  if (categories.size === 0) {
    throw new Error("Cannot merge nix/env.nix: no `<category> = [ ... ];` entries were found. Refusing " + "rather than emitting an empty skeleton that silently drops every package list.");
  }
  return { functionArgs, argSignature, withPackages, categories };
}
function mergeEnv(sortedFiles) {
  if (sortedFiles.length === 0) {
    throw new Error("Cannot merge nix/env.nix: no files were provided; at least one is required.");
  }
  const parsed = sortedFiles.map((f) => parseEnv(f.content));
  const firstArgs = parsed[0].functionArgs;
  const firstNormalized = parsed[0].argSignature;
  for (const p of parsed) {
    if (p.argSignature !== firstNormalized) {
      throw new Error(`env.nix function args mismatch: "${p.functionArgs}" vs "${firstArgs}"`);
    }
  }
  const firstWith = parsed[0].withPackages;
  for (const p of parsed) {
    if (p.withPackages !== firstWith) {
      throw new Error('env.nix "with packages;" presence mismatch across inputs');
    }
  }
  const mergedCategories = new Map;
  for (const p of parsed) {
    for (const [category, packages] of p.categories) {
      if (!mergedCategories.has(category)) {
        mergedCategories.set(category, new Set);
      }
      for (const pkg of packages) {
        mergedCategories.get(category).add(pkg);
      }
    }
  }
  return prettyPrint(firstArgs, firstWith, mergedCategories);
}
function prettyPrint(functionArgs, withPackages, categories) {
  const lines = [];
  lines.push(`${functionArgs}:`);
  if (withPackages) {
    lines.push("with packages;");
  }
  lines.push("{");
  const sortedCategories = [...categories.keys()].sort();
  for (let i = 0;i < sortedCategories.length; i++) {
    const cat = sortedCategories[i];
    const packages = [...categories.get(cat)].sort();
    if (i > 0) {
      lines.push("");
    }
    lines.push(`  ${cat} = [`);
    for (const pkg of packages) {
      lines.push(`    ${pkg}`);
    }
    lines.push("  ];");
  }
  lines.push("}");
  lines.push("");
  return lines.join(`
`);
}

// cyan/src/merge-fmt.ts
var FILE = "nix/fmt.nix";
function blank(value) {
  return value.replace(/[^\n]/g, " ");
}
function mask(source) {
  let out = "";
  let index = 0;
  while (index < source.length) {
    if (source[index] === "#") {
      const newline = source.indexOf(`
`, index);
      const stop = newline === -1 ? source.length : newline;
      out += blank(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith("/*", index)) {
      const close = source.indexOf("*/", index + 2);
      const stop = close === -1 ? source.length : close + 2;
      out += blank(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith("''", index)) {
      const close = source.indexOf("''", index + 2);
      const stop = close === -1 ? source.length : close + 2;
      out += "x".repeat(stop - index);
      index = stop;
      continue;
    }
    if (source[index] === '"') {
      let stop = index + 1;
      while (stop < source.length) {
        if (source[stop] === "\\") {
          stop += 2;
          continue;
        }
        stop++;
        if (source[stop - 1] === '"')
          break;
      }
      stop = Math.min(stop, source.length);
      out += "x".repeat(stop - index);
      index = stop;
      continue;
    }
    out += source[index];
    index++;
  }
  return out;
}
var CLOSERS = { "{": "}", "[": "]", "(": ")" };
function matchingIndex(code, open) {
  const openChar = code[open];
  const closeChar = CLOSERS[openChar];
  if (!closeChar)
    return -1;
  let depth = 0;
  for (let index = open;index < code.length; index++) {
    if (code[index] === openChar)
      depth++;
    else if (code[index] === closeChar) {
      depth--;
      if (depth === 0)
        return index;
    }
  }
  return -1;
}
function findKeyword(code, from, word) {
  const rest = code.slice(from);
  const match = new RegExp(`(?:^|[^a-zA-Z0-9_'.-])(${word})(?![a-zA-Z0-9_'-])`).exec(rest);
  if (!match)
    return -1;
  return from + match.index + match[0].lastIndexOf(word);
}
var IDENTIFIER_CHAR = /[a-zA-Z0-9_'-]/;
function isBareWord(code, index, word) {
  if (!code.startsWith(word, index))
    return false;
  const before = index > 0 ? code[index - 1] : "";
  if (before !== "" && (IDENTIFIER_CHAR.test(before) || before === "."))
    return false;
  const after = code[index + word.length] ?? "";
  return after === "" || !IDENTIFIER_CHAR.test(after);
}
function scanLetBody(code, from) {
  let depth = 0;
  let letDepth = 0;
  let fmtNameStart = -1;
  let fmtOpen = -1;
  for (let index = from;index < code.length; index++) {
    const char = code[index];
    if (char === "{" || char === "[" || char === "(") {
      depth++;
      continue;
    }
    if (char === "}" || char === "]" || char === ")") {
      depth--;
      continue;
    }
    if (isBareWord(code, index, "let")) {
      letDepth++;
      index += "let".length - 1;
      continue;
    }
    if (isBareWord(code, index, "in")) {
      if (letDepth === 0 && depth === 0)
        return { fmtNameStart, fmtOpen, inIndex: index };
      if (letDepth > 0)
        letDepth--;
      index += "in".length - 1;
      continue;
    }
    if (fmtOpen === -1 && depth === 0 && letDepth === 0 && isBareWord(code, index, "fmt")) {
      const match = /^fmt\s*=\s*\{/.exec(code.slice(index));
      if (match) {
        fmtNameStart = index;
        fmtOpen = index + match[0].lastIndexOf("{");
      }
    }
  }
  return { fmtNameStart, fmtOpen, inIndex: -1 };
}
function splitTopLevelCommas(value) {
  const code = mask(value);
  const parts = [];
  let start = 0;
  let depth = 0;
  for (let index = 0;index < code.length; index++) {
    const char = code[index];
    if (char === "{" || char === "[" || char === "(")
      depth++;
    else if (char === "}" || char === "]" || char === ")")
      depth--;
    else if (char === "," && depth === 0) {
      parts.push(value.slice(start, index));
      start = index + 1;
    }
  }
  parts.push(value.slice(start));
  return parts;
}
function trailingComment(gap) {
  const lines = gap.split(`
`);
  for (let index = lines.length - 1;index >= 0; index--) {
    const trimmed = lines[index].trim();
    if (trimmed === "")
      continue;
    return trimmed.startsWith("#") ? trimmed : undefined;
  }
  return;
}
function attrsetBody(value) {
  const trimmed = value.trim();
  if (!trimmed.startsWith("{"))
    return null;
  const close = matchingIndex(mask(trimmed), 0);
  return close === trimmed.length - 1 ? trimmed.slice(1, close) : null;
}
function listBody(value) {
  const trimmed = value.trim();
  if (!trimmed.startsWith("["))
    return null;
  const close = matchingIndex(mask(trimmed), 0);
  return close === trimmed.length - 1 ? trimmed.slice(1, close) : null;
}
function splitListItems(inner) {
  const code = mask(inner);
  const items = [];
  let index = 0;
  while (index < code.length) {
    while (index < code.length && /\s/.test(code[index]))
      index++;
    if (index >= code.length)
      break;
    const start = index;
    let depth = 0;
    while (index < code.length) {
      const char = code[index];
      if (char === "{" || char === "[" || char === "(")
        depth++;
      else if (char === "}" || char === "]" || char === ")")
        depth--;
      else if (depth === 0 && /\s/.test(char))
        break;
      index++;
    }
    items.push(inner.slice(start, index));
  }
  return items;
}
function trimBlankEdges(block) {
  const lines = block.split(`
`);
  while (lines.length > 0 && lines[0].trim() === "")
    lines.shift();
  while (lines.length > 0 && lines[lines.length - 1].trim() === "")
    lines.pop();
  return lines.join(`
`);
}
function reindent(block, indent) {
  const lines = block.split(`
`);
  const widths = lines.filter((line) => line.trim() !== "").map((line) => line.match(/^\s*/)[0].length);
  const common = widths.length > 0 ? Math.min(...widths) : 0;
  return lines.map((line) => line.trim() === "" ? "" : indent + line.slice(common));
}
function compare(a, b) {
  return a < b ? -1 : a > b ? 1 : 0;
}
function firstLine(value) {
  return value.split(`
`)[0].trim().slice(0, 60);
}
function splitAttrEntries(body, label, what) {
  const code = mask(body);
  const entries = [];
  let index = 0;
  while (index < code.length) {
    const gapStart = index;
    while (index < code.length && /\s/.test(code[index]))
      index++;
    if (index >= code.length)
      break;
    const keyMatch = /^([a-zA-Z_][a-zA-Z0-9_'-]*(?:\s*\.\s*[a-zA-Z_][a-zA-Z0-9_'-]*)*)\s*=(?!=)/.exec(code.slice(index));
    if (!keyMatch) {
      throw new Error(`${FILE}: could not parse ${what} \u2014 unrecognised entry starting "${firstLine(body.slice(index))}" in ${label}`);
    }
    const path = keyMatch[1].split(".").map((segment) => segment.trim());
    let cursor = index + keyMatch[0].length;
    const valueStart = cursor;
    let depth = 0;
    let letDepth = 0;
    let preludes = 0;
    while (cursor < code.length) {
      const char = code[cursor];
      if (char === "{" || char === "[" || char === "(")
        depth++;
      else if (char === "}" || char === "]" || char === ")")
        depth--;
      else if (isBareWord(code, cursor, "let")) {
        letDepth++;
        cursor += "let".length;
        continue;
      } else if (letDepth > 0 && isBareWord(code, cursor, "in")) {
        letDepth--;
        cursor += "in".length;
        continue;
      } else if (depth === 0 && letDepth === 0 && (isBareWord(code, cursor, "with") || isBareWord(code, cursor, "assert"))) {
        preludes++;
        cursor += isBareWord(code, cursor, "with") ? "with".length : "assert".length;
        continue;
      } else if (char === ";" && depth === 0 && letDepth === 0) {
        if (preludes === 0)
          break;
        preludes--;
      }
      cursor++;
    }
    if (cursor >= code.length) {
      throw new Error(`${FILE}: could not parse ${what} \u2014 binding "${path.join(".")}" is not terminated by ';' in ${label}`);
    }
    entries.push({
      path,
      value: body.slice(valueStart, cursor).trim(),
      comment: trailingComment(body.slice(gapStart, index))
    });
    index = cursor + 1;
  }
  return entries;
}
function classify(value) {
  const trimmed = value.trim();
  if (trimmed === "true")
    return { kind: "bool", value: true };
  if (trimmed === "false")
    return { kind: "bool", value: false };
  const list = listBody(trimmed);
  if (list !== null)
    return { kind: "list", items: splitListItems(list) };
  return { kind: "raw", text: trimmed };
}
function collectFields(prefix, value, fields, label, what) {
  const body = attrsetBody(value);
  if (body === null) {
    fields.set(prefix.join("."), classify(value));
    return;
  }
  const inner = splitAttrEntries(body, label, what);
  if (inner.length === 0) {
    fields.set(prefix.join("."), { kind: "raw", text: "{ }" });
    return;
  }
  for (const entry of inner) {
    collectFields([...prefix, ...entry.path], entry.value, fields, label, what);
  }
}
function addProgramEntry(programs, entry, label) {
  const what = "the 'programs' block";
  const [name, ...rest] = entry.path;
  let program = programs.get(name);
  if (!program) {
    program = { fields: new Map };
    programs.set(name, program);
  }
  if (entry.comment)
    program.comment = entry.comment;
  if (rest.length > 0) {
    program.raw = undefined;
    collectFields(rest, entry.value, program.fields, label, what);
    return;
  }
  const body = attrsetBody(entry.value);
  if (body === null) {
    program.fields.clear();
    program.raw = entry.value.trim();
    return;
  }
  program.raw = undefined;
  for (const inner of splitAttrEntries(body, label, what)) {
    collectFields(inner.path, inner.value, program.fields, label, what);
  }
}
function canonicalArgs(content, header) {
  const parts = splitTopLevelCommas(content.slice(header.openIndex + 1, header.closeIndex)).map((part) => part.trim()).filter((part) => part.length > 0 && part !== "...");
  const named = [...parts].sort((a, b) => compare(argName(a), argName(b)));
  const all = header.hasEllipsis ? [...named, "..."] : named;
  return all.length === 0 ? "{ }" : `{ ${all.join(", ")} }`;
}
function argName(part) {
  return part.match(/^([a-zA-Z_][a-zA-Z0-9_'-]*)/)?.[1] ?? part;
}
function parseFmt(content, label) {
  const code = mask(content);
  const header = findFunctionHeader(content);
  if (!header) {
    throw new Error(`${FILE}: could not parse the function header \u2014 expected an argument set followed by ':' ` + `at the start of the file, in ${label}`);
  }
  const functionArgs = canonicalArgs(content, header);
  const letIndex = findKeyword(code, header.colonIndex + 1, "let");
  if (letIndex === -1) {
    throw new Error(`${FILE}: could not find the 'let' block after the function header in ${label}`);
  }
  const letBodyStart = letIndex + "let".length;
  const { fmtNameStart, fmtOpen, inIndex } = scanLetBody(code, letBodyStart);
  if (fmtOpen === -1) {
    throw new Error(`${FILE}: could not find the 'fmt = { ... }' binding inside 'let' in ${label}`);
  }
  const fmtClose = matchingIndex(code, fmtOpen);
  if (fmtClose === -1) {
    throw new Error(`${FILE}: the 'fmt = { ... }' binding is never closed in ${label}`);
  }
  let afterFmt = fmtClose + 1;
  while (afterFmt < code.length && /\s/.test(code[afterFmt]))
    afterFmt++;
  if (code[afterFmt] === ";")
    afterFmt++;
  if (inIndex === -1) {
    throw new Error(`${FILE}: could not find the 'in' that closes the 'let' block in ${label}`);
  }
  const tail = content.slice(inIndex + "in".length).trim();
  if (tail === "") {
    throw new Error(`${FILE}: the expression after 'in' is empty in ${label}`);
  }
  const letPrefix = trimBlankEdges(content.slice(letBodyStart, fmtNameStart));
  const letSuffix = trimBlankEdges(content.slice(afterFmt, inIndex));
  const entries = splitAttrEntries(content.slice(fmtOpen + 1, fmtClose), label, "the 'fmt = { ... }' block");
  let projectRootFile = null;
  let projectRootFileComment;
  let programsComment;
  let hasPrograms = false;
  const programs = new Map;
  const unknownKeys = [];
  for (const entry of entries) {
    const [head, ...rest] = entry.path;
    if (head === "projectRootFile" && rest.length === 0) {
      projectRootFile = entry.value.trim();
      if (entry.comment)
        projectRootFileComment = entry.comment;
      continue;
    }
    if (head === "programs") {
      hasPrograms = true;
      if (entry.comment)
        programsComment = entry.comment;
      if (rest.length > 0) {
        addProgramEntry(programs, { path: rest, value: entry.value }, label);
        continue;
      }
      const body = attrsetBody(entry.value);
      if (body === null) {
        throw new Error(`${FILE}: 'programs' is not an attribute set in ${label}`);
      }
      for (const inner of splitAttrEntries(body, label, "the 'programs' block")) {
        addProgramEntry(programs, inner, label);
      }
      continue;
    }
    unknownKeys.push(entry.path.join("."));
  }
  if (projectRootFile === null) {
    throw new Error(`${FILE}: no 'projectRootFile' binding inside 'fmt = { ... }' in ${label}`);
  }
  return {
    functionArgs,
    projectRootFile,
    projectRootFileComment,
    programs,
    hasPrograms,
    programsComment,
    letPrefix,
    letSuffix,
    tail,
    unknownKeys
  };
}
function mergeFmt(sortedFiles) {
  if (sortedFiles.length === 0) {
    throw new Error("Cannot merge nix/fmt.nix: no files were provided; at least one is required.");
  }
  const labels = sortedFiles.map((file, index) => `layer ${index} (template: ${file.template})`);
  const parsed = sortedFiles.map((file, index) => parseFmt(file.content, labels[index]));
  for (let i = 0;i < parsed.length; i++) {
    const unknown = parsed[i].unknownKeys[0];
    if (unknown !== undefined) {
      throw new Error(`${FILE}: unknown top-level key "${unknown}" in ${labels[i]}`);
    }
  }
  const functionArgs = parsed[0].functionArgs;
  for (let i = 1;i < parsed.length; i++) {
    if (parsed[i].functionArgs !== functionArgs) {
      throw new Error(`${FILE}: function args mismatch \u2014 expected ${functionArgs}, got ${parsed[i].functionArgs} in ${labels[i]}`);
    }
  }
  const programs = new Map;
  for (const layer of parsed) {
    for (const [name, program] of layer.programs) {
      let merged = programs.get(name);
      if (!merged) {
        merged = { fields: new Map };
        programs.set(name, merged);
      }
      if (program.comment)
        merged.comment = program.comment;
      if (program.raw !== undefined) {
        merged.fields.clear();
        merged.raw = program.raw;
        continue;
      }
      merged.raw = undefined;
      for (const [path, value] of program.fields) {
        const previous = merged.fields.get(path);
        if (value.kind === "bool" && previous?.kind === "bool") {
          merged.fields.set(path, { kind: "bool", value: previous.value || value.value });
          continue;
        }
        merged.fields.set(path, value);
      }
    }
  }
  const highest = parsed[parsed.length - 1];
  return prettyPrint2({
    functionArgs,
    projectRootFile: highest.projectRootFile,
    projectRootFileComment: lastDefined(parsed.map((p) => p.projectRootFileComment)),
    programs,
    hasPrograms: parsed.some((p) => p.hasPrograms),
    programsComment: lastDefined(parsed.map((p) => p.programsComment)),
    letPrefix: highest.letPrefix,
    letSuffix: highest.letSuffix,
    tail: highest.tail,
    unknownKeys: []
  });
}
function lastDefined(values) {
  for (let index = values.length - 1;index >= 0; index--) {
    if (values[index] !== undefined)
      return values[index];
  }
  return;
}
function renderField(indent, key, value) {
  if (value.kind === "bool")
    return [`${indent}${key} = ${value.value};`];
  if (value.kind === "list") {
    if (value.items.length === 0)
      return [`${indent}${key} = [ ];`];
    if (value.items.length === 1)
      return [`${indent}${key} = [ ${value.items[0]} ];`];
    return [
      `${indent}${key} = [`,
      ...value.items.map((item) => `${indent}  ${item}`),
      `${indent}];`
    ];
  }
  return `${indent}${key} = ${value.text};`.split(`
`);
}
function renderProgram(name, program) {
  const lines = [];
  if (program.comment)
    lines.push(`      ${program.comment}`);
  if (program.raw !== undefined) {
    lines.push(...`      ${name} = ${program.raw};`.split(`
`));
    return lines;
  }
  const fields = program.fields;
  if (fields.size === 0) {
    lines.push(`      ${name} = { };`);
    return lines;
  }
  const enable = fields.get("enable");
  if (fields.size === 1 && enable?.kind === "bool" && enable.value) {
    lines.push(`      ${name}.enable = true;`);
    return lines;
  }
  lines.push(`      ${name} = {`);
  if (enable)
    lines.push(...renderField("        ", "enable", enable));
  for (const key of [...fields.keys()].sort(compare)) {
    if (key === "enable")
      continue;
    lines.push(...renderField("        ", key, fields.get(key)));
  }
  lines.push("      };");
  return lines;
}
function prettyPrint2(model) {
  const lines = [];
  lines.push(`${model.functionArgs}:`);
  lines.push("let");
  if (model.letPrefix)
    lines.push(...reindent(model.letPrefix, "  "));
  lines.push("  fmt = {");
  if (model.projectRootFileComment)
    lines.push(`    ${model.projectRootFileComment}`);
  lines.push(`    projectRootFile = ${model.projectRootFile};`);
  lines.push("");
  if (model.hasPrograms) {
    if (model.programsComment)
      lines.push(`    ${model.programsComment}`);
    lines.push("    programs = {");
    for (const name of [...model.programs.keys()].sort(compare)) {
      lines.push(...renderProgram(name, model.programs.get(name)));
    }
    lines.push("    };");
    lines.push("");
  }
  lines.push("  };");
  if (model.letSuffix)
    lines.push(...reindent(model.letSuffix, "  "));
  lines.push("in");
  lines.push(model.tail);
  lines.push("");
  return lines.join(`
`);
}

// cyan/src/merge-precommit.ts
function findMatchingBrace2(text, openIdx) {
  let depth = 0;
  let inString = false;
  for (let i = openIdx;i < text.length; i++) {
    const char = text[i];
    if (inString) {
      if (char === "\\") {
        i++;
      } else if (char === '"') {
        inString = false;
      }
      continue;
    }
    if (char === '"') {
      inString = true;
    } else if (char === "#") {
      const newline = text.indexOf(`
`, i);
      if (newline === -1)
        break;
      i = newline;
    } else if (char === "{") {
      depth++;
    } else if (char === "}") {
      depth--;
      if (depth === 0)
        return i;
    }
  }
  return -1;
}
function extractQuotedStrings(arrContent) {
  const strings = [];
  const matches = arrContent.matchAll(/"([^"]*)"/g);
  for (const match of matches) {
    strings.push(match[1]);
  }
  return strings;
}
var NIX_IDENTIFIER_FIELDS = ["package"];
function needsQuotes(key, value) {
  if (NIX_IDENTIFIER_FIELDS.includes(key)) {
    return false;
  }
  return true;
}
function parsePrecommit(content) {
  const headerStart = content.search(/\S/);
  if (headerStart === -1 || content[headerStart] !== "{") {
    throw new Error("pre-commit.nix function header not found");
  }
  const headerEnd = findMatchingBrace2(content, headerStart);
  if (headerEnd === -1 || content.slice(headerEnd + 1).match(/^\s*:/) === null) {
    throw new Error("pre-commit.nix function header not found");
  }
  const colonOffset = content.slice(headerEnd + 1).search(/:/);
  const bodyStart = headerEnd + 1 + colonOffset + 1;
  const functionArgs = content.slice(headerStart, headerEnd + 1).trim();
  const body = content.slice(bodyStart);
  const runMatch = /\bpre-commit-lib\.run\s*\{/.exec(body);
  if (!runMatch || runMatch.index === undefined) {
    throw new Error("pre-commit.nix pre-commit-lib.run block not found");
  }
  const runBlockStart = bodyStart + runMatch.index;
  const runOpen = content.indexOf("{", runBlockStart);
  const runBlockEnd = findMatchingBrace2(content, runOpen);
  if (runBlockEnd === -1) {
    throw new Error("pre-commit.nix pre-commit-lib.run block is not closed");
  }
  const prelude = content.slice(bodyStart, runBlockStart).trim();
  const runBlockContent = content.slice(runBlockStart, runBlockEnd + 1);
  let src = "./.";
  const srcMatch = runBlockContent.match(/\bsrc\s*=\s*([^;]+);/);
  if (srcMatch) {
    src = srcMatch[1].trim();
  }
  const hooks = parseHooks(runBlockContent);
  const hasRec = detectRecHooks(runBlockContent);
  return { functionArgs, prelude, src, hooks, hasRec };
}
function detectRecHooks(blockContent) {
  const hasRec = new Map;
  const recPattern = /^(\s*)([\w-]+)\s*=\s*rec\s*\{/gm;
  let match;
  while ((match = recPattern.exec(blockContent)) !== null) {
    hasRec.set(match[2], true);
  }
  return hasRec;
}
function parseHooks(blockContent) {
  const hooks = new Map;
  const hooksMatch = blockContent.match(/hooks\s*=\s*\{/);
  if (!hooksMatch)
    return hooks;
  const startIdx = blockContent.indexOf("{", hooksMatch.index);
  let depth = 0;
  let i = startIdx;
  let started = false;
  while (i < blockContent.length) {
    const char = blockContent[i];
    if (char === "{") {
      if (!started)
        started = true;
      depth++;
    } else if (char === "}") {
      depth--;
      if (started && depth === 0) {
        break;
      }
    }
    i++;
  }
  const hooksBlock = blockContent.slice(startIdx + 1, i);
  const lines = hooksBlock.split(`
`);
  let currentHook = null;
  let currentConfig = null;
  let inMultiLine = false;
  let inArrayField = null;
  let arrayContent = [];
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed)
      continue;
    if (inArrayField) {
      if (trimmed === "];") {
        if (inArrayField === "excludes") {
          currentConfig.excludes = [...arrayContent];
        } else if (inArrayField === "stages") {
          currentConfig.stages = [...arrayContent];
        }
        inArrayField = null;
        arrayContent = [];
        continue;
      }
      const itemMatch = trimmed.match(/^\s*"([^"]+)"\s*$/);
      if (itemMatch) {
        arrayContent.push(itemMatch[1]);
      }
      continue;
    }
    const singleLineMatch = trimmed.match(/^([\w-]+)\.enable\s*=\s*(true|false)\s*;?\s*$/);
    if (singleLineMatch) {
      if (currentHook && currentConfig) {
        hooks.set(currentHook, currentConfig);
      }
      hooks.set(singleLineMatch[1], { enable: singleLineMatch[2] === "true" });
      currentHook = null;
      currentConfig = null;
      inMultiLine = false;
      continue;
    }
    const multiStartMatch = trimmed.match(/^([\w-]+)\s*=\s*\{\s*$/);
    if (multiStartMatch) {
      if (currentHook && currentConfig) {
        hooks.set(currentHook, currentConfig);
      }
      currentHook = multiStartMatch[1];
      currentConfig = { enable: false };
      inMultiLine = true;
      continue;
    }
    const multiRecStartMatch = trimmed.match(/^([\w-]+)\s*=\s*rec\s*\{\s*$/);
    if (multiRecStartMatch) {
      if (currentHook && currentConfig) {
        hooks.set(currentHook, currentConfig);
      }
      currentHook = multiRecStartMatch[1];
      currentConfig = { enable: false };
      inMultiLine = true;
      continue;
    }
    if (inMultiLine && currentConfig) {
      if (trimmed === "};") {
        if (currentHook && currentConfig) {
          hooks.set(currentHook, currentConfig);
          currentHook = null;
          currentConfig = null;
          inMultiLine = false;
        }
        continue;
      }
      const enableMatch = trimmed.match(/^enable\s*=\s*(true|false)\s*;?\s*$/);
      if (enableMatch) {
        currentConfig.enable = enableMatch[1] === "true";
        continue;
      }
      const nameMatch = trimmed.match(/^name\s*=\s*"([^"]+)"\s*;?\s*$/);
      if (nameMatch) {
        currentConfig.name = nameMatch[1];
        continue;
      }
      const descMatch = trimmed.match(/^description\s*=\s*"([^"]+)"\s*;?\s*$/);
      if (descMatch) {
        currentConfig.description = descMatch[1];
        continue;
      }
      const entryMatch = trimmed.match(/^entry\s*=\s*"([^"]+)"\s*;?\s*$/);
      if (entryMatch) {
        currentConfig.entry = entryMatch[1];
        continue;
      }
      const filesMatch = trimmed.match(/^files\s*=\s*"([^"]+)"\s*;?\s*$/);
      if (filesMatch) {
        currentConfig.files = filesMatch[1];
        continue;
      }
      const langMatch = trimmed.match(/^language\s*=\s*"([^"]+)"\s*;?\s*$/);
      if (langMatch) {
        currentConfig.language = langMatch[1];
        continue;
      }
      const packageMatch = trimmed.match(/^package\s*=\s*([^\s;]+)\s*;?\s*$/);
      if (packageMatch) {
        currentConfig.package = packageMatch[1];
        continue;
      }
      const passMatch = trimmed.match(/^pass_filenames\s*=\s*(true|false)\s*;?\s*$/);
      if (passMatch) {
        currentConfig.pass_filenames = passMatch[1] === "true";
        continue;
      }
      const excludesInlineMatch = trimmed.match(/^excludes\s*=\s*\[(.*)\]\s*;?\s*$/);
      if (excludesInlineMatch) {
        const arrayContent2 = excludesInlineMatch[1].trim();
        if (arrayContent2) {
          currentConfig.excludes = extractQuotedStrings(arrayContent2);
        } else {
          currentConfig.excludes = [];
        }
        continue;
      }
      if (trimmed.startsWith("excludes = [")) {
        inArrayField = "excludes";
        arrayContent = [];
        continue;
      }
      const stagesInlineMatch = trimmed.match(/^stages\s*=\s*\[(.*)\]\s*;?\s*$/);
      if (stagesInlineMatch) {
        const arrayContent2 = stagesInlineMatch[1].trim();
        if (arrayContent2) {
          currentConfig.stages = extractQuotedStrings(arrayContent2);
        } else {
          currentConfig.stages = [];
        }
        continue;
      }
      if (trimmed.startsWith("stages = [")) {
        inArrayField = "stages";
        arrayContent = [];
        continue;
      }
      const passthroughMatch = trimmed.match(/^([a-zA-Z_][a-zA-Z0-9_-]*)\s*=\s*([^;]+)\s*;?\s*$/);
      if (passthroughMatch) {
        const key = passthroughMatch[1];
        const value = passthroughMatch[2].trim();
        if (value === "true" || value === "false") {
          currentConfig[key] = value === "true";
        } else if (value.startsWith('"') && value.endsWith('"')) {
          currentConfig[key] = value.slice(1, -1);
        } else {
          currentConfig[`__raw__${key}`] = value;
        }
        continue;
      }
    }
  }
  if (currentHook && currentConfig) {
    hooks.set(currentHook, currentConfig);
  }
  return hooks;
}
function mergePrecommit(sortedFiles) {
  const parsed = sortedFiles.map((f) => parsePrecommit(f.content));
  function normalizeFunctionArgs(args) {
    const match = args.match(/^\{([^}]+)\}$/);
    if (!match)
      return args;
    const argList = match[1].split(",").map((a) => a.trim()).filter(Boolean);
    const restIdx = argList.indexOf("...");
    let sorted;
    if (restIdx !== -1) {
      const rest = argList.splice(restIdx, 1);
      sorted = [...argList.sort(), ...rest];
    } else {
      sorted = argList.sort();
    }
    return `{ ${sorted.join(", ")} }`;
  }
  const firstArgs = normalizeFunctionArgs(parsed[0].functionArgs);
  for (const p of parsed) {
    const otherArgs = normalizeFunctionArgs(p.functionArgs);
    if (otherArgs !== firstArgs) {
      throw new Error(`pre-commit.nix function args mismatch: "${otherArgs}" vs "${firstArgs}"`);
    }
  }
  const src = parsed[parsed.length - 1].src;
  const mergedHooks = new Map;
  const mergedRec = new Map;
  for (const p of parsed) {
    for (const [name, config] of p.hooks) {
      if (!mergedHooks.has(name)) {
        mergedHooks.set(name, { enable: false });
      }
      const existing = mergedHooks.get(name);
      if (config.enable) {
        existing.enable = true;
      }
      if (config.name !== undefined)
        existing.name = config.name;
      if (config.description !== undefined)
        existing.description = config.description;
      if (config.entry !== undefined)
        existing.entry = config.entry;
      if (config.files !== undefined)
        existing.files = config.files;
      if (config.language !== undefined)
        existing.language = config.language;
      if (config.package !== undefined)
        existing.package = config.package;
      if (config.pass_filenames !== undefined)
        existing.pass_filenames = config.pass_filenames;
      if (config.excludes !== undefined) {
        if (!existing.excludes)
          existing.excludes = [];
        for (const ex of config.excludes) {
          if (!existing.excludes.includes(ex))
            existing.excludes.push(ex);
        }
      }
      if (config.stages !== undefined) {
        if (!existing.stages)
          existing.stages = [];
        for (const st of config.stages) {
          if (!existing.stages.includes(st))
            existing.stages.push(st);
        }
      }
      for (const [key, value] of Object.entries(config)) {
        if (!["enable", "name", "description", "entry", "files", "language", "package", "pass_filenames", "excludes", "stages"].includes(key)) {
          if (key.startsWith("__raw__")) {
            const realKey = key.slice(7);
            delete existing[realKey];
          } else {
            delete existing[`__raw__${key}`];
          }
          existing[key] = value;
        }
      }
    }
    for (const [name, hasRec] of p.hasRec) {
      if (hasRec) {
        mergedRec.set(name, true);
      }
    }
  }
  for (const config of mergedHooks.values()) {
    if (config.excludes)
      config.excludes.sort();
    if (config.stages)
      config.stages.sort();
  }
  const prelude = parsed[parsed.length - 1].prelude;
  return prettyPrint3(firstArgs, prelude, src, mergedHooks, mergedRec);
}
function prettyPrint3(functionArgs, prelude, src, hooks, hasRec) {
  const lines = [];
  const sortedArgs = sortFunctionArgs(functionArgs);
  lines.push(`${sortedArgs}:`);
  if (prelude) {
    lines.push(prelude);
  }
  lines.push("pre-commit-lib.run {");
  lines.push(`  src = ${src};`);
  lines.push("");
  lines.push("  hooks = {");
  const sortedHookNames = [...hooks.keys()].sort();
  for (let i = 0;i < sortedHookNames.length; i++) {
    const name = sortedHookNames[i];
    const config = hooks.get(name);
    const isRec = hasRec.get(name) ?? false;
    if (i > 0) {
      lines.push("");
    }
    const isSingleLine = Object.keys(config).length === 1 && "enable" in config;
    if (isSingleLine) {
      lines.push(`    ${name}.enable = ${config.enable};`);
    } else {
      if (isRec) {
        lines.push(`    ${name} = rec {`);
      } else {
        lines.push(`    ${name} = {`);
      }
      lines.push(`      enable = ${config.enable};`);
      const fieldOrder = ["description", "entry", "excludes", "files", "language", "name", "package", "pass_filenames", "stages"];
      const emittedKeys = new Set(["enable"]);
      for (const key of fieldOrder) {
        emittedKeys.add(key);
        if (key === "excludes" && config.excludes) {
          lines.push(`      excludes = [`);
          for (const ex of config.excludes) {
            lines.push(`        "${ex}"`);
          }
          lines.push("      ];");
        } else if (key === "stages" && config.stages) {
          lines.push(`      stages = [`);
          for (const st of config.stages) {
            lines.push(`        "${st}"`);
          }
          lines.push("      ];");
        } else if (key in config && config[key] !== undefined) {
          const value = config[key];
          if (typeof value === "string") {
            if (needsQuotes(key, value)) {
              lines.push(`      ${key} = "${value}";`);
            } else {
              lines.push(`      ${key} = ${value};`);
            }
          } else if (typeof value === "boolean") {
            lines.push(`      ${key} = ${value};`);
          }
        }
      }
      for (const key of Object.keys(config).sort()) {
        if (emittedKeys.has(key))
          continue;
        if (key.startsWith("__raw__"))
          continue;
        const value = config[key];
        if (value === undefined)
          continue;
        if (typeof value === "string") {
          if (needsQuotes(key, value)) {
            lines.push(`      ${key} = "${value}";`);
          } else {
            lines.push(`      ${key} = ${value};`);
          }
        } else if (typeof value === "boolean") {
          lines.push(`      ${key} = ${value};`);
        }
      }
      for (const key of Object.keys(config).sort()) {
        if (!key.startsWith("__raw__"))
          continue;
        const value = config[key];
        if (typeof value === "string") {
          const realKey = key.slice(7);
          lines.push(`      ${realKey} = ${value};`);
        }
      }
      lines.push("    };");
    }
  }
  lines.push("  };");
  lines.push("}");
  lines.push("");
  return lines.join(`
`);
}
function sortFunctionArgs(argsStr) {
  const match = argsStr.match(/^\{([^}]+)\}$/);
  if (!match)
    return argsStr;
  const argsContent = match[1];
  const args = argsContent.split(",").map((arg) => arg.trim());
  const restIdx = args.indexOf("...");
  let sortedArgs;
  if (restIdx !== -1) {
    const rest = args.splice(restIdx, 1);
    sortedArgs = [...args.sort(), ...rest];
  } else {
    sortedArgs = args.sort();
  }
  return `{ ${sortedArgs.join(", ")} }`;
}

// cyan/src/merge-packages.ts
function findMatching(text, openChar, closeChar, openIdx) {
  let depth = 1;
  let i = openIdx + 1;
  while (i < text.length && depth > 0) {
    const skipped = skipLexical(text, i);
    if (skipped !== i) {
      i = skipped;
      continue;
    }
    if (text[i] === openChar)
      depth++;
    else if (text[i] === closeChar)
      depth--;
    if (depth === 0)
      return i;
    i++;
  }
  return -1;
}
function skipLexical(text, pos) {
  if (pos >= text.length)
    return pos;
  const ch = text[pos];
  if (ch === "#") {
    let i = pos;
    while (i < text.length && text[i] !== `
`)
      i++;
    return i;
  }
  if (ch === '"') {
    let i = pos + 1;
    while (i < text.length) {
      if (text[i] === "\\") {
        i += 2;
        continue;
      }
      if (text[i] === '"')
        return i + 1;
      i++;
    }
    return text.length;
  }
  if (ch === "'" && pos + 1 < text.length && text[pos + 1] === "'") {
    let i = pos + 2;
    while (i < text.length) {
      if (text[i] === "$" && i + 1 < text.length && text[i + 1] === "{") {
        i += 2;
        let depth = 1;
        while (i < text.length && depth > 0) {
          if (text[i] === "{")
            depth++;
          else if (text[i] === "}")
            depth--;
          i++;
        }
        continue;
      }
      if (text[i] === "'" && i + 2 < text.length && text[i + 1] === "'" && text[i + 2] === "'") {
        i += 3;
        continue;
      }
      if (text[i] === "'" && i + 1 < text.length && text[i + 1] === "'") {
        return i + 2;
      }
      i++;
    }
    return text.length;
  }
  return pos;
}
function parsePackages(content) {
  const header = findFunctionHeader(content);
  if (!header) {
    throw new Error("Cannot merge nix/packages.nix: no function argument set was found. The file must " + "begin with a `{ ... }:` head (single-line or multi-line); refusing rather than " + "emitting a headless skeleton.");
  }
  const functionArgs = [...header.args];
  const argSources = header.argSources;
  const hasEllipsis = header.hasEllipsis;
  const code = maskNixTrivia(content);
  const subBlocks = new Map;
  const allMatch = code.search(/\ball\s*=\s*rec\s*\{/);
  if (allMatch === -1) {
    throw new Error("Cannot merge nix/packages.nix: the `all = rec { ... }` registry block was not " + "found. This merger only models that shape; refusing rather than emitting an " + "empty skeleton that silently drops every package.");
  }
  const braceStart = code.indexOf("{", allMatch);
  const closingIdx = findMatching(content, "{", "}", braceStart);
  if (closingIdx === -1) {
    throw new Error("Cannot merge nix/packages.nix: the `all = rec { ... }` block is unbalanced \u2014 its " + "closing brace was not found.");
  }
  const outerBody = content.slice(braceStart + 1, closingIdx);
  parseSubBlocks(outerBody, subBlocks);
  let mergeLine = [];
  const inRegex = /\bin\s*\n/gs;
  const inMatches = [...code.matchAll(inRegex)];
  let afterIn = "";
  if (inMatches.length > 0) {
    const lastMatch = inMatches[inMatches.length - 1];
    afterIn = content.slice(lastMatch.index + lastMatch[0].length).trim();
  }
  if (afterIn) {
    for (const line of afterIn.split(`
`)) {
      const trimmed = line.trim();
      if (!trimmed)
        continue;
      if (trimmed.startsWith("with ") && trimmed.endsWith(";"))
        continue;
      const parts = trimmed.split("//");
      for (const part of parts) {
        const name = part.trim();
        if (name)
          mergeLine.push(name);
      }
    }
  }
  if (mergeLine.length === 0) {
    throw new Error("Cannot merge nix/packages.nix: no final `with all; a // b` merge expression was " + "found after `in`. Refusing rather than emitting a file whose registry blocks are " + "never combined.");
  }
  return { functionArgs, argSources, hasEllipsis, subBlocks, mergeLine };
}
function parseSubBlocks(outerBody, subBlocks) {
  let pos = 0;
  while (pos < outerBody.length) {
    while (pos < outerBody.length) {
      if (/\s/.test(outerBody[pos])) {
        pos++;
        continue;
      }
      const skipped = skipLexical(outerBody, pos);
      if (skipped !== pos) {
        pos = skipped;
        continue;
      }
      break;
    }
    if (pos >= outerBody.length)
      break;
    const match = outerBody.slice(pos).match(/^([\w-]+)\s*=\s*\(/);
    if (!match) {
      pos++;
      continue;
    }
    const blockName = match[1];
    pos += match[0].length;
    const parenClose = findMatching(outerBody, "(", ")", pos - 1);
    if (parenClose === -1)
      break;
    const parenBody = outerBody.slice(pos, parenClose).trim();
    pos = parenClose + 1;
    while (pos < outerBody.length && outerBody[pos] === ";")
      pos++;
    let withRegistry = null;
    let hasRec = false;
    let innerBraceOpen = -1;
    let innerBraceClose = -1;
    const withMatch = parenBody.match(/^with\s+([\w-]+)\s*;/);
    if (withMatch) {
      withRegistry = withMatch[1];
    }
    const recIdx = parenBody.indexOf("rec {");
    const plainBraceIdx = parenBody.indexOf("{");
    if (recIdx !== -1) {
      hasRec = true;
      innerBraceOpen = recIdx + 4;
    } else if (plainBraceIdx !== -1) {
      innerBraceOpen = plainBraceIdx;
    }
    if (innerBraceOpen !== -1) {
      innerBraceClose = findMatching(parenBody, "{", "}", innerBraceOpen);
    }
    const entries = [];
    if (innerBraceOpen !== -1 && innerBraceClose !== -1) {
      const innerBody = parenBody.slice(innerBraceOpen + 1, innerBraceClose);
      parseEntries(innerBody, entries);
    }
    subBlocks.set(blockName, { name: blockName, withRegistry, hasRec, entries });
  }
}
function parseEntries(innerBody, entries) {
  let pos = 0;
  let pendingComment;
  while (pos < innerBody.length) {
    while (pos < innerBody.length) {
      if (/\s/.test(innerBody[pos])) {
        pos++;
        continue;
      }
      if (innerBody[pos] === "#") {
        const lineEnd = innerBody.indexOf(`
`, pos);
        pendingComment = innerBody.slice(pos, lineEnd === -1 ? innerBody.length : lineEnd).trim();
        pos = lineEnd === -1 ? innerBody.length : lineEnd + 1;
        continue;
      }
      const skipped = skipLexical(innerBody, pos);
      if (skipped !== pos) {
        pos = skipped;
        continue;
      }
      break;
    }
    if (pos >= innerBody.length)
      break;
    if (/^inherit(?=[\s;}])/.test(innerBody.slice(pos))) {
      pos += 7;
      let wsEnd = pos;
      while (wsEnd < innerBody.length && (innerBody[wsEnd] === " " || innerBody[wsEnd] === "\t"))
        wsEnd++;
      let semiPos = -1;
      let newlinePos = -1;
      for (let i = wsEnd;i < innerBody.length; i++) {
        if (innerBody[i] === `
` && newlinePos === -1)
          newlinePos = i;
        if (innerBody[i] === ";") {
          semiPos = i;
          break;
        }
        if (innerBody[i] === "}")
          break;
      }
      if (semiPos !== -1 && (newlinePos === -1 || semiPos < newlinePos)) {
        const idStr = innerBody.slice(wsEnd, semiPos).trim();
        const ids = idStr.split(/\s+/).filter((id) => id.length > 0 && !id.startsWith("#"));
        entries.push({ type: "inherit", identifiers: ids.map((id) => ({ id })) });
        pendingComment = undefined;
        pos = semiPos + 1;
      } else {
        const ids = [];
        while (pos < innerBody.length) {
          while (pos < innerBody.length && /\s/.test(innerBody[pos]))
            pos++;
          if (pos >= innerBody.length)
            break;
          if (innerBody[pos] === ";") {
            pos++;
            break;
          }
          if (innerBody[pos] === "}")
            break;
          if (innerBody[pos] === "#") {
            let commentStart = pos;
            while (pos < innerBody.length && innerBody[pos] !== `
`)
              pos++;
            pendingComment = innerBody.slice(commentStart, pos).trim();
            continue;
          }
          let idStart = pos;
          while (pos < innerBody.length && !/\s/.test(innerBody[pos]) && innerBody[pos] !== ";" && innerBody[pos] !== "#")
            pos++;
          const id = innerBody.slice(idStart, pos);
          const commentIdx = id.indexOf("#");
          const cleanId = commentIdx > 0 ? id.slice(0, commentIdx).trim() : id;
          if (cleanId && !cleanId.startsWith("#")) {
            ids.push({ id: cleanId, comment: pendingComment });
            pendingComment = undefined;
          }
        }
        entries.push({ type: "inherit", identifiers: ids });
      }
      continue;
    }
    const assignMatch = innerBody.slice(pos).match(/^([\w-]+)\s*=\s*/);
    if (assignMatch) {
      const name = assignMatch[1];
      pos += assignMatch[0].length;
      let valueStart = pos;
      let depth = 0;
      while (pos < innerBody.length) {
        const skipped = skipLexical(innerBody, pos);
        if (skipped !== pos) {
          pos = skipped;
          continue;
        }
        const ch = innerBody[pos];
        if (ch === "{" || ch === "(")
          depth++;
        else if (ch === "}" || ch === ")") {
          if (depth === 0)
            break;
          depth--;
        }
        if (ch === ";" && depth === 0)
          break;
        pos++;
      }
      const value = innerBody.slice(valueStart, pos).trim();
      if (value.endsWith(";")) {}
      pos++;
      entries.push({ type: "assignment", name, value, comment: pendingComment });
      pendingComment = undefined;
      continue;
    }
    pos++;
  }
}
function mergePackages(sortedFiles) {
  if (sortedFiles.length === 0) {
    throw new Error("Cannot merge nix/packages.nix: no files were provided; at least one is required.");
  }
  const parsed = sortedFiles.map((f) => parsePackages(f.content));
  const allArgs = new Set;
  const argSources = new Map;
  let hasEllipsis = false;
  for (const p of parsed) {
    for (const arg of p.functionArgs) {
      allArgs.add(arg);
      const source = p.argSources.get(arg);
      if (source !== undefined)
        argSources.set(arg, source.replace(/\s+/g, " "));
    }
    if (p.hasEllipsis)
      hasEllipsis = true;
  }
  const functionArgs = [...allArgs].sort().map((name) => argSources.get(name) ?? name);
  const mergedBlocks = new Map;
  const inheritIdMaps = new Map;
  for (const p of parsed) {
    for (const [name, block] of p.subBlocks) {
      if (!mergedBlocks.has(name)) {
        mergedBlocks.set(name, {
          name,
          withRegistry: block.withRegistry,
          hasRec: block.hasRec,
          entries: []
        });
      }
      const existing = mergedBlocks.get(name);
      if (block.withRegistry !== null && existing.withRegistry !== null && existing.withRegistry !== block.withRegistry) {
        throw new Error(`Cannot merge sub-block "${name}" with conflicting registries: ` + `${existing.withRegistry} vs ${block.withRegistry}`);
      }
      if (block.withRegistry !== null && existing.withRegistry === null) {
        existing.withRegistry = block.withRegistry;
      }
      if (block.hasRec) {
        existing.hasRec = true;
      }
      if (!inheritIdMaps.has(name))
        inheritIdMaps.set(name, new Map);
      const inheritMap = inheritIdMaps.get(name);
      for (const entry of block.entries) {
        if (entry.type === "inherit") {
          for (const ident of entry.identifiers ?? []) {
            inheritMap.set(ident.id, ident);
          }
        } else if (entry.type === "assignment" && entry.name) {
          const existingIdx = existing.entries.findIndex((e) => e.type === "assignment" && e.name === entry.name);
          if (existingIdx >= 0) {
            existing.entries[existingIdx] = entry;
          } else {
            existing.entries.push(entry);
          }
        }
      }
    }
  }
  for (const [name, inheritMap] of inheritIdMaps) {
    const block = mergedBlocks.get(name);
    if (block && inheritMap.size > 0) {
      const sorted = [...inheritMap.values()].sort((a, b) => a.id.localeCompare(b.id));
      block.entries.push({ type: "inherit", identifiers: sorted });
    }
  }
  const mergeNames = new Set;
  for (const p of parsed) {
    for (const name of p.mergeLine) {
      mergeNames.add(name);
    }
  }
  const mergeLine = [...mergeNames].sort();
  return prettyPrint4(functionArgs, hasEllipsis, mergedBlocks, mergeLine);
}
function prettyPrint4(functionArgs, hasEllipsis, subBlocks, mergeLine) {
  const lines = [];
  const headArgs = hasEllipsis ? [...functionArgs, "..."] : functionArgs;
  lines.push(`{ ${headArgs.join(", ")} }:`);
  lines.push("let");
  lines.push("  all = rec {");
  const sortedBlockNames = [...subBlocks.keys()].sort();
  for (let bi = 0;bi < sortedBlockNames.length; bi++) {
    const blockName = sortedBlockNames[bi];
    const block = subBlocks.get(blockName);
    if (bi > 0) {
      lines.push("");
    }
    const assignments = block.entries.filter((e) => e.type === "assignment").sort((a, b) => (a.name ?? "").localeCompare(b.name ?? ""));
    const inherits = block.entries.filter((e) => e.type === "inherit");
    const inheritIdMap = new Map;
    for (const inh of inherits) {
      for (const ident of inh.identifiers ?? []) {
        inheritIdMap.set(ident.id, ident);
      }
    }
    const sortedInheritIds = [...inheritIdMap.values()].sort((a, b) => a.id.localeCompare(b.id));
    lines.push(`    ${blockName} = (`);
    if (block.withRegistry) {
      lines.push(`      with ${block.withRegistry};`);
    }
    if (block.hasRec) {
      lines.push("      rec {");
    } else {
      lines.push("      {");
    }
    for (let ai = 0;ai < assignments.length; ai++) {
      if (ai > 0) {
        lines.push("");
      }
      if (assignments[ai].comment) {
        lines.push(`        ${assignments[ai].comment}`);
      }
      const valueLines = formatAssignmentValue(assignments[ai].name, assignments[ai].value);
      for (const vl of valueLines) {
        lines.push("        " + vl);
      }
    }
    if (sortedInheritIds.length > 0) {
      if (assignments.length > 0) {
        lines.push("");
      }
      lines.push("        inherit");
      for (const ident of sortedInheritIds) {
        if (ident.comment) {
          lines.push("");
          lines.push(`          ${ident.comment}`);
        }
        lines.push(`          ${ident.id}`);
      }
      lines.push("        ;");
    }
    lines.push("      }");
    lines.push("    );");
  }
  lines.push("  };");
  lines.push("in");
  lines.push("with all;");
  for (let mi = 0;mi < mergeLine.length; mi++) {
    if (mi === mergeLine.length - 1) {
      lines.push(mergeLine[mi]);
    } else {
      lines.push(`${mergeLine[mi]} //`);
    }
  }
  lines.push("");
  return lines.join(`
`);
}
function formatAssignmentValue(name, value) {
  const trimmed = value.trim();
  if (!trimmed.includes(`
`)) {
    return [`${name} = ${trimmed};`];
  }
  const valueLines = trimmed.split(`
`);
  const result = [`${name} = ${valueLines[0]}`];
  for (let i = 1;i < valueLines.length - 1; i++) {
    result.push(valueLines[i]);
  }
  result.push(`${valueLines[valueLines.length - 1]};`);
  return result;
}

// cyan/src/merge-shells.ts
function matchingBrace(code, open) {
  let depth = 0;
  for (let i = open;i < code.length; i++) {
    if (code[i] === "{")
      depth++;
    else if (code[i] === "}") {
      depth--;
      if (depth === 0)
        return i;
    }
  }
  return -1;
}
function skipWhitespace(code, pos) {
  let i = pos;
  while (i < code.length && /\s/.test(code[i]))
    i++;
  return i;
}
function entryTerminator(code, pos, limit) {
  let depth = 0;
  for (let i = pos;i < limit; i++) {
    const ch = code[i];
    if (ch === "{" || ch === "[" || ch === "(")
      depth++;
    else if (ch === "}" || ch === "]" || ch === ")") {
      if (depth === 0)
        return -1;
      depth--;
    } else if (ch === ";" && depth === 0)
      return i;
  }
  return -1;
}
function splitConcat(content, code, start, limit) {
  const parts = [];
  let depth = 0;
  let segment = start;
  for (let i = start;i < limit; i++) {
    const ch = code[i];
    if (ch === "(" || ch === "[" || ch === "{")
      depth++;
    else if (ch === ")" || ch === "]" || ch === "}")
      depth--;
    else if (depth === 0 && ch === "+" && code[i + 1] === "+") {
      parts.push(content.slice(segment, i));
      i++;
      segment = i + 1;
    }
  }
  parts.push(content.slice(segment, limit));
  return parts.map((p) => p.trim()).filter(Boolean);
}
function refuse(detail) {
  throw new Error(`Cannot merge nix/shells.nix: ${detail}`);
}
function parseShells(content) {
  const code = maskNixTrivia(content);
  const header = findFunctionHeader(content);
  if (!header) {
    refuse("no function argument set was found. The file must begin with a `{ ... }:` head " + "(single-line or multi-line); refusing rather than emitting a headless skeleton.");
  }
  const preludes = [];
  let pos = skipWhitespace(code, header.colonIndex + 1);
  for (;; ) {
    const match = code.slice(pos).match(/^with\s+([a-zA-Z_][\w'-]*(?:\s*\.\s*[a-zA-Z_][\w'-]*)*)\s*;/);
    if (!match)
      break;
    preludes.push(match[1].replace(/\s+/g, ""));
    pos = skipWhitespace(code, pos + match[0].length);
  }
  if (code[pos] !== "{") {
    refuse("the top-level shell attribute set was not found after the function head. " + "Expected `{ <name> = pkgs.mkShell { ... }; ... }`; refusing rather than emitting " + "an empty skeleton.");
  }
  const attrsetOpen = pos;
  const attrsetClose = matchingBrace(code, attrsetOpen);
  if (attrsetClose === -1) {
    refuse("the top-level shell attribute set is unbalanced \u2014 its closing brace was not found.");
  }
  const shells = new Map;
  let cursor = skipWhitespace(code, attrsetOpen + 1);
  while (cursor < attrsetClose) {
    const rest = code.slice(cursor, attrsetClose);
    const shellMatch = rest.match(/^([\w-]+)\s*=\s*pkgs\.mkShell\s*\{/);
    if (!shellMatch) {
      const offender = content.slice(cursor, Math.min(cursor + 60, attrsetClose)).trim().split(`
`)[0];
      refuse(`unrecognised entry "${offender}" in the top-level attribute set. Every entry must ` + "be `<name> = pkgs.mkShell { ... };`; refusing rather than dropping it.");
    }
    const name = shellMatch[1];
    const bodyOpen = cursor + shellMatch[0].length - 1;
    const bodyClose = matchingBrace(code, bodyOpen);
    if (bodyClose === -1 || bodyClose > attrsetClose) {
      refuse(`shell "${name}" has an unbalanced \`pkgs.mkShell { ... }\` block.`);
    }
    shells.set(name, parseShellBody(content, code, bodyOpen + 1, bodyClose, name));
    cursor = skipWhitespace(code, bodyClose + 1);
    if (code[cursor] !== ";") {
      refuse(`shell "${name}" is not terminated by a \`;\` after its \`pkgs.mkShell\` block.`);
    }
    cursor = skipWhitespace(code, cursor + 1);
  }
  if (shells.size === 0) {
    refuse("the top-level attribute set declares no shells. Refusing rather than emitting an " + "empty skeleton that silently drops every dev shell.");
  }
  return {
    functionArgs: [...header.args],
    argSources: header.argSources,
    hasEllipsis: header.hasEllipsis,
    preludes,
    shells
  };
}
function parseShellBody(content, code, start, limit, name) {
  const buildInputs = [];
  const inherits = [];
  let cursor = skipWhitespace(code, start);
  while (cursor < limit) {
    const terminator = entryTerminator(code, cursor, limit);
    if (terminator === -1) {
      refuse(`an entry inside shell "${name}" is not terminated by a \`;\`.`);
    }
    const entry = content.slice(cursor, terminator);
    const entryCode = code.slice(cursor, terminator);
    const inheritMatch = entryCode.match(/^inherit\b([\s\S]*)$/);
    if (inheritMatch) {
      if (inheritMatch[1].trimStart().startsWith("(")) {
        refuse(`shell "${name}" uses \`inherit (<source>) ...;\`, which this merger does not ` + "model. Refusing rather than re-scoping the inherited names.");
      }
      for (const identifier of inheritMatch[1].match(/[a-zA-Z_][\w'-]*/g) ?? []) {
        if (!inherits.includes(identifier))
          inherits.push(identifier);
      }
    } else {
      const assignment = entryCode.match(/^([\w-]+)\s*=\s*/);
      if (!assignment) {
        refuse(`unrecognised entry "${entry.trim().split(`
`)[0]}" inside shell "${name}".`);
      }
      if (assignment[1] !== "buildInputs") {
        refuse(`unknown field "${assignment[1]}" inside shell "${name}" \u2014 only "buildInputs" and ` + "`inherit ...;` are modelled. Refusing rather than dropping it from the merged output.");
      }
      const rhsStart = cursor + assignment[0].length;
      for (const part of splitConcat(content, code, rhsStart, terminator)) {
        buildInputs.push(part);
      }
    }
    cursor = skipWhitespace(code, terminator + 1);
  }
  return { buildInputs, inherits };
}
function mergeShells(sortedFiles) {
  if (sortedFiles.length === 0) {
    refuse("no files were provided; at least one is required.");
  }
  const parsed = sortedFiles.map((f) => parseShells(f.content));
  const rendered = (p) => p.functionArgs.map((name) => (p.argSources.get(name) ?? name).replace(/\s+/g, " "));
  const signature = (p) => `{ ${[...rendered(p)].sort().join(", ")}${p.hasEllipsis ? ", ..." : ""} }`;
  const firstSignature = signature(parsed[0]);
  for (const p of parsed) {
    if (signature(p) !== firstSignature) {
      refuse(`function args mismatch: "${signature(p)}" vs "${firstSignature}"`);
    }
  }
  const firstPreludes = parsed[0].preludes.join(", ");
  for (const p of parsed) {
    if (p.preludes.join(", ") !== firstPreludes) {
      refuse(`prelude mismatch across inputs: "${p.preludes.map((w) => `with ${w};`).join(" ") || "(none)"}" ` + `vs "${parsed[0].preludes.map((w) => `with ${w};`).join(" ") || "(none)"}"`);
    }
  }
  const mergedInputs = new Map;
  const mergedInherits = new Map;
  for (const p of parsed) {
    for (const [name, shell] of p.shells) {
      if (!mergedInputs.has(name)) {
        mergedInputs.set(name, new Set);
        mergedInherits.set(name, new Set);
      }
      for (const input of shell.buildInputs)
        mergedInputs.get(name).add(input);
      for (const identifier of shell.inherits)
        mergedInherits.get(name).add(identifier);
    }
  }
  return prettyPrint5([...rendered(parsed[0])].sort(), parsed[0].hasEllipsis, parsed[0].preludes, mergedInputs, mergedInherits);
}
function prettyPrint5(functionArgs, hasEllipsis, preludes, shells, inherits) {
  const lines = [];
  const headArgs = hasEllipsis ? [...functionArgs, "..."] : functionArgs;
  lines.push(`{ ${headArgs.join(", ")} }:`);
  for (const prelude of preludes) {
    lines.push(`with ${prelude};`);
  }
  lines.push("{");
  const sortedShellNames = [...shells.keys()].sort();
  for (let si = 0;si < sortedShellNames.length; si++) {
    const shellName = sortedShellNames[si];
    const buildInputs = [...shells.get(shellName)].sort();
    const shellInherits = [...inherits.get(shellName) ?? new Set].sort();
    if (si > 0) {
      lines.push("");
    }
    lines.push(`  ${shellName} = pkgs.mkShell {`);
    lines.push(`    buildInputs = ${buildInputs.length === 0 ? "[]" : buildInputs.join(" ++ ")};`);
    if (shellInherits.length > 0) {
      lines.push(`    inherit ${shellInherits.join(" ")};`);
    }
    lines.push("  };");
  }
  lines.push("}");
  lines.push("");
  return lines.join(`
`);
}

// index.ts
var MERGERS = Object.fromEntries(Object.entries({
  "flake.nix": mergeFlake,
  "nix/env.nix": mergeEnv,
  "nix/fmt.nix": mergeFmt,
  "nix/packages.nix": mergePackages,
  "nix/shells.nix": mergeShells,
  "nix/pre-commit.nix": mergePrecommit
}).map(([file, merge]) => [file, withLossGuard(file, merge)]));
async function resolver(input) {
  const { files } = input;
  if (files.length === 0)
    throw new Error("Resolver received no files \u2014 at least 1 file is required");
  const uniquePaths = new Set(files.map((f) => f.path));
  if (uniquePaths.size > 1)
    throw new Error(`Resolver received files with different paths: ${[...uniquePaths].join(", ")} \u2014 all files must have the same path`);
  const path = files[0].path;
  const sorted = [...files].sort((a, b) => {
    if (a.origin.layer !== b.origin.layer)
      return a.origin.layer - b.origin.layer;
    return a.origin.template.localeCompare(b.origin.template);
  });
  const basename = path.split("/").pop() ?? path;
  const fullRelPath = path.includes("/") ? path : basename;
  const merger = MERGERS[basename] ?? MERGERS[fullRelPath];
  const variations = sorted.map((f) => ({
    content: f.content,
    layer: f.origin.layer,
    template: f.origin.template
  }));
  if (merger) {
    return { path, content: merger(variations) };
  }
  if (variations.length === 1) {
    return { path, content: variations[0].content };
  }
  throw new Error(`atomi/nix has no merger for "${path}" but received ${variations.length} variations. ` + `The path is outside the dispatch table (${Object.keys(MERGERS).join(", ")}); ` + `merging it would silently last-write-wins while recording resolver-merged. ` + `Add a merger for this path, or remove it from the resolver's files: globs.`);
}
export {
  resolver
};
