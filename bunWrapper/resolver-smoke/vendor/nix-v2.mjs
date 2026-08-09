// @bun
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
function parseWithRecAssignments(content) {
  const match = content.match(/with\s+rec\s*\{/);
  if (!match)
    return [];
  const braceStart = match.index + match[0].length;
  const closingIdx = findMatchingBrace(content, braceStart);
  if (closingIdx === -1)
    return [];
  const body = content.slice(braceStart, closingIdx);
  const assignments = [];
  let currentName = "";
  let currentBody = "";
  let depth = 0;
  let inAssignment = false;
  let pos = 0;
  while (pos < body.length) {
    const char = body[pos];
    if (!inAssignment) {
      if (char === "=" && currentName.trim()) {
        inAssignment = true;
        pos++;
        continue;
      }
      if (char === "#") {
        const nlIdx = body.indexOf(`
`, pos);
        pos = nlIdx === -1 ? body.length : nlIdx + 1;
        currentName = "";
        continue;
      }
      if (char !== " " && char !== `
` && char !== "\t" && char !== ";") {
        currentName += char;
      }
      pos++;
    } else {
      if (char === "{")
        depth++;
      else if (char === "}") {
        depth--;
        if (depth < 0) {
          currentBody = currentBody.trim();
          break;
        }
      }
      currentBody += char;
      pos++;
      if (char === ";" && depth === 0) {
        let body2 = currentBody.trim();
        body2 = body2.replace(/\s*;\s*$/, "");
        assignments.push({
          name: currentName.trim(),
          body: body2
        });
        currentName = "";
        currentBody = "";
        depth = 0;
        inAssignment = false;
      }
    }
  }
  if (currentName.trim() && currentBody.trim()) {
    let body2 = currentBody.trim();
    body2 = body2.replace(/\s*;\s*$/, "");
    assignments.push({
      name: currentName.trim(),
      body: body2
    });
  }
  return assignments;
}
function parseFinalInheritIds(content) {
  const withRecMatch = content.match(/with\s+rec\s*\{/);
  if (!withRecMatch)
    return [];
  const braceStart = withRecMatch.index + withRecMatch[0].length;
  const closingIdx = findMatchingBrace(content, braceStart);
  if (closingIdx === -1)
    return [];
  const afterRec = content.slice(closingIdx);
  const inheritMatch = afterRec.match(/\{\s*inherit\s+([^;]+);\s*\}/);
  if (!inheritMatch)
    return [];
  return inheritMatch[1].trim().split(/\s+/);
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
function findBracedRegion(content, pattern) {
  const match = content.match(pattern);
  if (!match)
    return null;
  const relativeOpen = match[0].lastIndexOf("{");
  const open = match.index + relativeOpen;
  const close = findMatchingBrace(content, open + 1);
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
  const withRecRegion = findBracedRegion(content, /with\s+rec\s*\{/);
  if (!withRecRegion)
    throw new Error("Cannot merge flake.nix: with rec block was not found");
  const withRecBody = content.slice(withRecRegion.open + 1, withRecRegion.close);
  const packagesMatch = withRecBody.match(/\bpackages\s*=\s*import\b/);
  if (!packagesMatch)
    return content;
  const packagesStart = withRecRegion.open + 1 + packagesMatch.index;
  const argsOpen = content.indexOf("{", packagesStart + packagesMatch[0].length);
  if (argsOpen === -1 || argsOpen >= withRecRegion.close) {
    throw new Error("Cannot merge flake.nix: packages import argument set was not found");
  }
  const argsClose = findMatchingBrace(content, argsOpen + 1);
  if (argsClose === -1 || argsClose > withRecRegion.close) {
    throw new Error("Cannot merge flake.nix: packages import argument set is unbalanced");
  }
  const argsBody = content.slice(argsOpen + 1, argsClose);
  const inheritMatch = argsBody.match(/\binherit\b([\s\S]*?);/);
  if (!inheritMatch)
    return content;
  const existingIds = new Set((inheritMatch[1].match(/[a-zA-Z_][\w'-]*/g) ?? []).filter((id) => id !== "inherit"));
  const missing = mergedIds.filter((id) => !existingIds.has(id)).sort();
  if (missing.length === 0)
    return content;
  const semicolon = argsOpen + 1 + inheritMatch.index + inheritMatch[0].lastIndexOf(";");
  const inheritText = inheritMatch[1];
  if (!inheritText.includes(`
`)) {
    return content.slice(0, semicolon) + " " + missing.join(" ") + content.slice(semicolon);
  }
  const semicolonLineStart = content.lastIndexOf(`
`, semicolon) + 1;
  const beforeSemicolon = content.slice(semicolonLineStart, semicolon);
  if (/^\s*$/.test(beforeSemicolon)) {
    const indent = beforeSemicolon;
    const block = missing.map((id) => indent + id).join(`
`);
    return content.slice(0, semicolonLineStart) + block + `
` + content.slice(semicolonLineStart);
  }
  return content.slice(0, semicolon) + " " + missing.join(" ") + content.slice(semicolon);
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
  let content = sortedFiles[sortedFiles.length - 1].content;
  content = insertMissingInputs(content, mergedInputs);
  content = insertMissingOutputParams(content, mergedOutputParams, mergedOutputExpressions);
  content = insertMissingRegistryLines(content, mergedRegistries, pkgsAlias);
  content = insertMissingPackageInherits(content, [...allPackageInherits]);
  assertMergeInvariants(content, mergedInputs, mergedOutputParams);
  return content;
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
  const lines = content.split(`
`);
  let functionArgs = "";
  let lineIdx = 0;
  const argsMatch = lines[0]?.match(/^\s*(\{[^}]+\})\s*:\s*$/);
  if (argsMatch) {
    functionArgs = argsMatch[1];
    lineIdx = 1;
  }
  let withPackages = false;
  if (lineIdx < lines.length) {
    const withMatch = lines[lineIdx].match(/^\s*with\s+packages\s*;\s*$/);
    if (withMatch) {
      withPackages = true;
      lineIdx++;
    }
  }
  const categories = new Map;
  let currentCategory = null;
  let inList = false;
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
  return { functionArgs, withPackages, categories };
}
function mergeEnv(sortedFiles) {
  const parsed = sortedFiles.map((f) => parseEnv(f.content));
  const firstArgs = parsed[0].functionArgs;
  for (const p of parsed) {
    if (p.functionArgs !== firstArgs) {
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
function normalizeFunctionArgs(argsStr) {
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
function parseFmt(content) {
  const lines = content.split(`
`);
  let functionArgs = "";
  let lineIdx = 0;
  const argsMatch = lines[0]?.match(/^\s*(\{[^}]+\})\s*:\s*$/);
  if (argsMatch) {
    functionArgs = argsMatch[1];
    lineIdx = 1;
  }
  let inFmtBlock = false;
  let fmtBlockStart = -1;
  let braceDepth = 0;
  for (let i = lineIdx;i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();
    if (!inFmtBlock) {
      if (trimmed === "let") {
        for (let j = i + 1;j < lines.length; j++) {
          const nextTrimmed = lines[j].trim();
          const fmtMatch = nextTrimmed.match(/^fmt\s*=\s*\{/);
          if (fmtMatch) {
            inFmtBlock = true;
            fmtBlockStart = j;
            braceDepth = 1;
            lineIdx = j + 1;
            break;
          }
        }
        if (!inFmtBlock)
          break;
      }
    }
    if (fmtBlockStart !== -1 && i >= fmtBlockStart) {
      for (const char of lines[i]) {
        if (char === "{")
          braceDepth++;
        else if (char === "}")
          braceDepth--;
      }
      if (braceDepth === 0 && i > fmtBlockStart) {
        lineIdx = i + 1;
        break;
      }
    }
  }
  let fmtBlockContent = "";
  if (fmtBlockStart !== -1) {
    let depth = 0;
    let started = false;
    let endLine = lines.length;
    for (let i = fmtBlockStart;i < lines.length; i++) {
      const line = lines[i];
      for (const char of line) {
        if (char === "{") {
          if (!started)
            started = true;
          depth++;
        } else if (char === "}") {
          depth--;
          if (started && depth === 0) {
            endLine = i;
            break;
          }
        }
      }
      if (depth === 0 && started)
        break;
    }
    fmtBlockContent = lines.slice(fmtBlockStart, endLine + 1).join(`
`);
    lineIdx = endLine + 1;
  }
  let projectRootFile = "";
  const projectRootMatch = fmtBlockContent.match(/projectRootFile\s*=\s*"([^"]+)"/);
  if (projectRootMatch) {
    projectRootFile = projectRootMatch[1];
  }
  const programs = parsePrograms(fmtBlockContent);
  let programsComment;
  const programsLineIdx = fmtBlockContent.indexOf("programs = {");
  if (programsLineIdx !== -1) {
    const beforePrograms = fmtBlockContent.slice(0, programsLineIdx);
    const lines2 = beforePrograms.split(`
`);
    for (let i = lines2.length - 1;i >= 0; i--) {
      const trimmedLine = lines2[i].trim();
      if (trimmedLine === "")
        continue;
      if (trimmedLine.startsWith("#")) {
        programsComment = trimmedLine;
      }
      break;
    }
  }
  let tail = "";
  const inIdx = content.indexOf(`
in`);
  if (inIdx !== -1) {
    tail = content.slice(inIdx + 3).trim();
  }
  return { functionArgs, projectRootFile, programs, tail, programsComment };
}
function parsePrograms(blockContent) {
  const programs = new Map;
  const programsMatch = blockContent.match(/programs\s*=\s*\{/);
  if (!programsMatch)
    return programs;
  const startIdx = programsMatch.index + programsMatch[0].length;
  let depth = 1;
  let i = startIdx;
  while (i < blockContent.length && depth > 0) {
    if (blockContent[i] === "{")
      depth++;
    else if (blockContent[i] === "}")
      depth--;
    i++;
  }
  const programsBlock = blockContent.slice(startIdx, i - 1);
  const lines = programsBlock.split(`
`);
  let currentProgram = null;
  let currentConfig = null;
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed)
      continue;
    if (trimmed === "}") {
      if (currentProgram && currentConfig) {
        programs.set(currentProgram, currentConfig);
        currentProgram = null;
        currentConfig = null;
      }
      continue;
    }
    const singleLineMatch = trimmed.match(/^([a-zA-Z0-9_-]+)\.enable\s*=\s*(true|false)\s*;?\s*$/);
    if (singleLineMatch) {
      if (currentProgram && currentConfig) {
        programs.set(currentProgram, currentConfig);
      }
      programs.set(singleLineMatch[1], { enable: singleLineMatch[2] === "true" });
      currentProgram = null;
      currentConfig = null;
      continue;
    }
    const multiStartMatch = trimmed.match(/^([a-zA-Z0-9_-]+)\s*=\s*\{\s*$/);
    if (multiStartMatch) {
      if (currentProgram && currentConfig) {
        programs.set(currentProgram, currentConfig);
      }
      currentProgram = multiStartMatch[1];
      currentConfig = { enable: false };
      continue;
    }
    if (currentConfig) {
      const enableMatch = trimmed.match(/^enable\s*=\s*(true|false)\s*;?\s*$/);
      if (enableMatch) {
        currentConfig.enable = enableMatch[1] === "true";
        continue;
      }
      const extraArgsMatch = trimmed.match(/^extra_args\s*=\s*\[\s*(.*?)\s*\]\s*;?\s*$/);
      if (extraArgsMatch) {
        const arrayContent = extraArgsMatch[1];
        const args = [];
        const stringMatches = arrayContent.matchAll(/"([^"]*)"/g);
        for (const match of stringMatches) {
          args.push(match[1]);
        }
        currentConfig.extra_args = args;
        continue;
      }
      const boolMatch = trimmed.match(/^([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*(true|false)\s*;?\s*$/);
      if (boolMatch) {
        currentConfig[boolMatch[1]] = boolMatch[2] === "true";
        continue;
      }
      if (trimmed === "};") {
        if (currentProgram && currentConfig) {
          programs.set(currentProgram, currentConfig);
          currentProgram = null;
          currentConfig = null;
        }
        continue;
      }
    }
  }
  return programs;
}
function mergeFmt(sortedFiles) {
  const parsed = sortedFiles.map((f) => parseFmt(f.content));
  const normalizedArgs = normalizeFunctionArgs(parsed[0].functionArgs);
  for (let i = 1;i < parsed.length; i++) {
    const otherArgs = normalizeFunctionArgs(parsed[i].functionArgs);
    if (otherArgs !== normalizedArgs) {
      throw new Error(`fmt.nix: function args mismatch \u2014 expected ${normalizedArgs}, got ${otherArgs} in layer ${i}`);
    }
  }
  const projectRootFile = parsed[parsed.length - 1].projectRootFile;
  for (let i = 0;i < parsed.length; i++) {
    const content = sortedFiles[i].content;
    const fmtMatch = content.match(/fmt\s*=\s*\{/);
    if (!fmtMatch)
      continue;
    const fmtStart = fmtMatch.index + fmtMatch[0].length;
    let depth = 1;
    let j = fmtStart;
    while (j < content.length && depth > 0) {
      if (content[j] === "{")
        depth++;
      else if (content[j] === "}")
        depth--;
      j++;
    }
    if (depth === 0) {
      const fmtBlock = content.slice(fmtMatch.index, j);
      const sections = fmtBlock.split(/(programs\s*=\s*\{)/);
      const beforePrograms = sections[0] ?? "";
      const linesBefore = beforePrograms.split(`
`);
      const unknownKeysBefore = [...linesBefore.slice(1).join(`
`).matchAll(/^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=/gm)];
      for (const match of unknownKeysBefore) {
        const key = match[1];
        if (key !== "projectRootFile") {
          throw new Error(`fmt.nix: unknown top-level key "${key}" in layer ${i} (template: ${sortedFiles[i].template})`);
        }
      }
      if (sections.length > 2) {
        const afterProgramsHeader = sections.slice(2).join("");
        let progDepth = 1;
        let progEnd = 0;
        for (let k = 0;k < afterProgramsHeader.length; k++) {
          if (afterProgramsHeader[k] === "{")
            progDepth++;
          else if (afterProgramsHeader[k] === "}") {
            progDepth--;
            if (progDepth === 0) {
              progEnd = k + 1;
              break;
            }
          }
        }
        if (progEnd > 0) {
          const afterPrograms = afterProgramsHeader.slice(progEnd);
          const unknownKeysAfter = [...afterPrograms.matchAll(/^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=/gm)];
          for (const match of unknownKeysAfter) {
            const key = match[1];
            if (key !== "projectRootFile") {
              throw new Error(`fmt.nix: unknown top-level key "${key}" in layer ${i} (template: ${sortedFiles[i].template})`);
            }
          }
        }
      }
    }
  }
  const mergedPrograms = new Map;
  for (const p of parsed) {
    for (const [name, config] of p.programs) {
      if (!mergedPrograms.has(name)) {
        mergedPrograms.set(name, { enable: false });
      }
      const existing = mergedPrograms.get(name);
      if (config.enable) {
        existing.enable = true;
      }
      if (config.extra_args !== undefined) {
        existing.extra_args = config.extra_args;
      }
      for (const [key, value] of Object.entries(config)) {
        if (key !== "enable" && key !== "extra_args" && typeof value === "boolean" && value) {
          existing[key] = value;
        }
      }
    }
  }
  return prettyPrint2(normalizedArgs, projectRootFile, mergedPrograms, parsed[parsed.length - 1].tail, parsed[parsed.length - 1].programsComment);
}
function prettyPrint2(functionArgs, projectRootFile, programs, tail, programsComment) {
  const lines = [];
  lines.push(`${functionArgs}:`);
  lines.push("let");
  lines.push("  fmt = {");
  lines.push(`    projectRootFile = "${projectRootFile}";`);
  lines.push("");
  if (programsComment) {
    lines.push(`    ${programsComment}`);
  }
  lines.push("    programs = {");
  const sortedPrograms = [...programs.keys()].sort();
  for (let i = 0;i < sortedPrograms.length; i++) {
    const name = sortedPrograms[i];
    const config = programs.get(name);
    const isSingleLine = Object.keys(config).length === 1 && config.enable === true;
    const otherKeys = Object.entries(config).filter(([k]) => k !== "enable").sort(([a], [b]) => a.localeCompare(b));
    if (isSingleLine) {
      lines.push(`      ${name}.enable = true;`);
    } else {
      lines.push(`      ${name} = {`);
      lines.push(`        enable = ${config.enable};`);
      for (const [key, value] of otherKeys) {
        if (key === "extra_args" && Array.isArray(value)) {
          const quotedArgs = value.map((v) => `"${v}"`).join(" ");
          lines.push(`        extra_args = [ ${quotedArgs} ];`);
        } else if (typeof value === "boolean") {
          lines.push(`        ${key} = ${value};`);
        }
      }
      lines.push("      };");
    }
  }
  lines.push("    };");
  lines.push("");
  lines.push("  };");
  lines.push("in");
  lines.push(tail);
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
  function normalizeFunctionArgs2(args) {
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
  const firstArgs = normalizeFunctionArgs2(parsed[0].functionArgs);
  for (const p of parsed) {
    const otherArgs = normalizeFunctionArgs2(p.functionArgs);
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
  const lines = content.split(`
`);
  let functionArgs = [];
  let lineIdx = 0;
  const argsMatch = lines[0]?.match(/^\s*\{([^}]+)\}\s*:\s*$/);
  if (argsMatch) {
    functionArgs = argsMatch[1].split(",").map((a) => a.trim()).filter(Boolean);
    lineIdx = 1;
  }
  for (let i = lineIdx;i < lines.length; i++) {
    if (lines[i].trim() === "let") {
      lineIdx = i + 1;
      break;
    }
  }
  const subBlocks = new Map;
  const allMatch = content.indexOf("all = rec {");
  if (allMatch !== -1) {
    const braceStart = allMatch + "all = rec ".length;
    const closingIdx = findMatching(content, "{", "}", braceStart);
    if (closingIdx !== -1) {
      const outerBody = content.slice(braceStart + 1, closingIdx);
      parseSubBlocks(outerBody, subBlocks);
    }
  }
  let mergeLine = [];
  const inRegex = /\bin\s*\n/gs;
  const inMatches = [...content.matchAll(inRegex)];
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
  return { functionArgs, subBlocks, mergeLine };
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
  const parsed = sortedFiles.map((f) => parsePackages(f.content));
  const allArgs = new Set;
  for (const p of parsed) {
    for (const arg of p.functionArgs) {
      allArgs.add(arg);
    }
  }
  const functionArgs = [...allArgs].sort();
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
  return prettyPrint4(functionArgs, mergedBlocks, mergeLine);
}
function prettyPrint4(functionArgs, subBlocks, mergeLine) {
  const lines = [];
  lines.push(`{ ${functionArgs.join(", ")} }:`);
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
function parseShells(content) {
  const lines = content.split(`
`);
  let functionArgs = [];
  let lineIdx = 0;
  const argsMatch = lines[0]?.match(/^\s*\{([^}]+)\}\s*:\s*$/);
  if (argsMatch) {
    functionArgs = argsMatch[1].split(",").map((a) => a.trim()).filter(Boolean);
    lineIdx = 1;
  }
  let withEnv = false;
  if (lineIdx < lines.length) {
    const withMatch = lines[lineIdx].match(/^\s*with\s+env\s*;\s*$/);
    if (withMatch) {
      withEnv = true;
      lineIdx++;
    }
  }
  const shells = new Map;
  let inAttrset = false;
  let currentShell = null;
  let currentBuildInputs = [];
  let inMkShell = false;
  let mkShellDepth = 0;
  for (let i = lineIdx;i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (trimmed === "{") {
      inAttrset = true;
      continue;
    }
    if (trimmed === "}") {
      if (inAttrset && currentShell !== null) {
        shells.set(currentShell, { buildInputs: currentBuildInputs });
        currentShell = null;
        currentBuildInputs = [];
      }
      break;
    }
    if (!inAttrset)
      continue;
    const shellMatch = trimmed.match(/^([\w-]+)\s*=\s*pkgs\.mkShell\s*\{/);
    if (shellMatch) {
      if (currentShell !== null) {
        shells.set(currentShell, { buildInputs: currentBuildInputs });
      }
      currentShell = shellMatch[1];
      currentBuildInputs = [];
      inMkShell = true;
      mkShellDepth = 1;
      continue;
    }
    if (inMkShell && currentShell) {
      for (const ch of trimmed) {
        if (ch === "{")
          mkShellDepth++;
        else if (ch === "}")
          mkShellDepth--;
      }
      const buildInputsMatch = trimmed.match(/^buildInputs\s*=\s*(.+);\s*$/);
      if (buildInputsMatch) {
        const rhs = buildInputsMatch[1];
        const parts = rhs.split("++").map((p) => p.trim()).filter(Boolean);
        currentBuildInputs.push(...parts);
      }
      if (trimmed.match(/^inherit\s+shellHook\s*;/))
        continue;
      if (trimmed && !trimmed.startsWith("#") && !buildInputsMatch && !trimmed.match(/^inherit\s+shellHook\s*;/)) {
        if (mkShellDepth > 0 && trimmed !== "}") {
          throw new Error(`shells.nix: unknown field "${trimmed.split(/[=;]/)[0].trim()}" inside shell "${currentShell}" \u2014 only "buildInputs" and "inherit shellHook;" are allowed`);
        }
      }
      if (mkShellDepth <= 0) {
        inMkShell = false;
      }
      continue;
    }
  }
  return { functionArgs, withEnv, shells };
}
function mergeShells(sortedFiles) {
  if (sortedFiles.length === 0) {
    throw new Error("shells.nix merge requires at least one input file");
  }
  const parsed = sortedFiles.map((f) => parseShells(f.content));
  const firstArgs = [...parsed[0].functionArgs].sort();
  for (const p of parsed) {
    const pArgs = [...p.functionArgs].sort();
    if (pArgs.length !== firstArgs.length || !pArgs.every((a, i) => a === firstArgs[i])) {
      throw new Error(`shells.nix function args mismatch: "[${p.functionArgs.join(", ")}]" vs "[${parsed[0].functionArgs.join(", ")}]"`);
    }
  }
  const firstWithEnv = parsed[0].withEnv;
  for (const p of parsed) {
    if (p.withEnv !== firstWithEnv) {
      throw new Error('shells.nix "with env;" presence mismatch across inputs');
    }
  }
  const mergedShells = new Map;
  for (const p of parsed) {
    for (const [name, shell] of p.shells) {
      if (!mergedShells.has(name)) {
        mergedShells.set(name, new Set);
      }
      for (const input of shell.buildInputs) {
        mergedShells.get(name).add(input);
      }
    }
  }
  const hasShellHook = parsed[0].functionArgs.includes("shellHook");
  return prettyPrint5(parsed[0].functionArgs, firstWithEnv, mergedShells, hasShellHook);
}
function prettyPrint5(functionArgs, withEnv, shells, hasShellHook) {
  const lines = [];
  const rest = functionArgs.filter((a) => a === "...");
  const namedArgs = functionArgs.filter((a) => a !== "...").sort();
  const sortedArgs = [...namedArgs, ...rest];
  lines.push(`{ ${sortedArgs.join(", ")} }:`);
  if (withEnv) {
    lines.push("with env;");
  }
  lines.push("{");
  const sortedShellNames = [...shells.keys()].sort();
  for (let si = 0;si < sortedShellNames.length; si++) {
    const shellName = sortedShellNames[si];
    const buildInputs = [...shells.get(shellName)].sort();
    if (si > 0) {
      lines.push("");
    }
    lines.push(`  ${shellName} = pkgs.mkShell {`);
    lines.push(`    buildInputs = ${buildInputs.length === 0 ? "[]" : buildInputs.join(" ++ ")};`);
    if (hasShellHook) {
      lines.push("    inherit shellHook;");
    }
    lines.push("  };");
  }
  lines.push("}");
  lines.push("");
  return lines.join(`
`);
}

// index.ts
var MERGERS = {
  "flake.nix": mergeFlake,
  "nix/env.nix": mergeEnv,
  "nix/fmt.nix": mergeFmt,
  "nix/packages.nix": mergePackages,
  "nix/shells.nix": mergeShells,
  "nix/pre-commit.nix": mergePrecommit
};
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
