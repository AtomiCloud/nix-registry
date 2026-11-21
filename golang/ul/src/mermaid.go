package main

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// mermaidSegs returns the labels a reader sees in a mermaid diagram: node and edge labels,
// subgraph and participant names, messages and titles. Ids, arrows, keywords and styling are
// never read. src starts at source line first. A construct this does not know is not read, so
// the miss is silent and a hit is never invented.
func mermaidSegs(src string, first int) []Seg {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	kind := ""
	var out []Seg
	add := func(label string, line int) {
		if label = strings.TrimSpace(decodeMermaidEntities(label)); label != "" {
			out = append(out, plainSeg(label, line))
		}
	}
	// A YAML preamble (--- ... ---) comes before the diagram header: only its title is read.
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start < len(lines) && strings.TrimSpace(lines[start]) == "---" {
		for e := start + 1; e < len(lines); e++ {
			if strings.TrimSpace(lines[e]) == "---" {
				for k := start + 1; k < e; k++ {
					if m := fmTitleRe.FindStringSubmatch(lines[k]); m != nil {
						add(strings.Trim(strings.TrimSpace(m[1]), "\"'"), first+k)
					}
				}
				start = e + 1
				break
			}
		}
	}
	for i := start; i < len(lines); i++ {
		whole := lines[i]
		if strings.HasPrefix(strings.TrimSpace(whole), "%%") {
			continue // a comment line holds no statement, whatever follows a semicolon
		}
		for _, raw := range splitStatements(whole) {
			line := first + i
			t := strings.TrimSpace(raw)
			if t == "" || strings.HasPrefix(t, "%%") {
				continue
			}
			if kind == "" {
				kind = diagramKind(t)
				if kind == "" {
					continue
				}
				// a pie writes its title on the header: pie showData title Pets
				if m := pieTitleRe.FindStringSubmatch(t); m != nil {
					add(strings.Trim(strings.TrimSpace(m[1]), "\""), line)
					continue
				}
			}
			if (kind == "flow" && skipFlow.MatchString(t)) || clickRe.MatchString(t) {
				continue // styling and click directives hold no label
			}
			if kind == "seq" && seqSkip.MatchString(t) {
				continue
			}
			// node metadata, A@{ img: "https://x.test/a.png", label: "Cookware" }: only the label is read
			t = nodeMetaRe.ReplaceAllStringFunc(t, func(block string) string {
				for _, v := range metaLabels(block) {
					add(v, line)
				}
				return " "
			})
			// quoted labels: "text"
			rest := t
			for _, m := range quotedRe.FindAllStringSubmatch(t, -1) {
				add(m[1], line)
			}
			rest = quotedRe.ReplaceAllString(rest, `""`)
			if m := titleRe.FindStringSubmatch(rest); m != nil {
				add(m[2], line)
				continue
			}
			switch kind {
			case "flow":
				if skipFlow.MatchString(rest) {
					continue
				}
				if m := subgraphRe.FindStringSubmatch(rest); m != nil {
					if m[2] == "" && !strings.ContainsAny(m[1], "[({") {
						add(m[1], line)
					}
					rest = m[2]
				}
				for _, m := range bracketRe.FindAllStringSubmatch(rest, -1) {
					add(strings.Trim(m[1], "/\\ "), line)
				}
				for _, m := range pipeRe.FindAllStringSubmatch(rest, -1) {
					add(m[1], line)
				}
				for _, re := range inlineEdgeRes {
					for _, m := range re.FindAllStringSubmatch(rest, -1) {
						add(m[1], line)
					}
				}
			case "seq":
				if seqSkip.MatchString(rest) {
					continue
				}
				if m := participantRe.FindStringSubmatch(rest); m != nil {
					add(m[2], line)
				} else if m := noteRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				} else if m := messageRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				}
			case "other":
				if m := relationRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				}
			case "state":
				if m := stateEdgeRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				} else if m := stateDescRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				} else if m := stateNoteRe.FindStringSubmatch(rest); m != nil {
					add(m[1], line)
				}
			}
		}
	}
	return out
}

func diagramKind(first string) string {
	w := strings.Fields(first)
	if len(w) == 0 {
		return ""
	}
	switch strings.ToLower(w[0]) {
	case "graph", "flowchart":
		return "flow"
	case "sequencediagram":
		return "seq"
	case "statediagram", "statediagram-v2":
		return "state"
	case "pie", "journey", "gantt", "timeline", "mindmap", "quadrantchart", "xychart-beta", "gitgraph", "classdiagram", "erdiagram", "requirementdiagram", "c4context", "sankey-beta", "block-beta":
		return "other"
	}
	return "other"
}

var (
	quotedRe      = regexp.MustCompile(`"([^"]*)"`)
	fmTitleRe     = regexp.MustCompile(`^\s*title\s*:\s*(.+?)\s*$`)
	entityRe      = regexp.MustCompile(`#([A-Za-z][A-Za-z0-9]*|[0-9]{1,7});`)
	entityEndRe   = regexp.MustCompile(`#[A-Za-z0-9]+$`)
	pieTitleRe    = regexp.MustCompile(`(?i)^pie\b.*?\btitle\s+(.+)$`)
	nodeMetaRe    = regexp.MustCompile(`@\{[^}]*\}`)
	titleRe       = regexp.MustCompile(`(?i)^\s*(title|section|accTitle|accDescr)\s*:?\s+(.+)$`)
	clickRe       = regexp.MustCompile(`(?i)^\s*click\b`)
	skipFlow      = regexp.MustCompile(`(?i)^\s*(classDef|class|style|linkStyle|click|direction|end)\b`)
	subgraphRe    = regexp.MustCompile(`(?i)^\s*subgraph\s+([^\[\]]*?)\s*(\[.*)?$`)
	bracketRe     = regexp.MustCompile(`[\[({>]+([^\[\](){}"|>]*[^\[\](){}"|>\s/\\][^\[\](){}"|]*?)[\])}]+`)
	pipeRe        = regexp.MustCompile(`\|([^|"]+)\|`)
	participantRe = regexp.MustCompile(`(?i)^\s*(participant|actor)\s+\S+\s+as\s+(.+)$`)
	noteRe        = regexp.MustCompile(`(?i)^\s*note\s+(?:left of|right of|over)\s+[^:]+:\s*(.+)$`)
	messageRe     = regexp.MustCompile(`^\s*[^\s:]+\s*(?:-->>|->>|-->|->|--x|-x|--\)|-\))[+-]?\s*[^\s:]+\s*:\s*(.+)$`)
	stateEdgeRe   = regexp.MustCompile(`^\s*[^\s:]+\s*-->\s*[^\s:]+\s*:\s*(.+)$`)
	stateDescRe   = regexp.MustCompile(`^\s*[A-Za-z0-9_\-]+\s*:\s*(.+)$`)
	relationRe    = regexp.MustCompile(`^\s*\S+\s*(?:--\|>|<\|--|\*--|o--|-->|\.\.>|\.\.\|>|--|\.\.)\s*\S+\s*:\s*(.+)$`)
	seqSkip       = regexp.MustCompile(`(?i)^\s*(links?|properties|details)\b`)
	stateNoteRe   = regexp.MustCompile(`(?i)^\s*note\s+(?:left of|right of)\s+\S+\s*:\s*(.+)$`)
	inlineEdgeRes = []*regexp.Regexp{
		regexp.MustCompile(`--\s+([^->|]+?)\s+-->`),
		regexp.MustCompile(`-\.\s+([^.>|]+?)\s+\.->`),
		regexp.MustCompile(`==\s+([^=>|]+?)\s+==>`),
	}
)

// metaLabels is the label of a node's @{ ... } metadata: the other keys (img, icon, shape, ...) hold
// URLs and names, never text a reader reads.
func metaLabels(block string) []string {
	body := strings.TrimSuffix(strings.TrimPrefix(block, "@{"), "}")
	var out []string
	inQuote, start := false, 0
	pair := func(s string) {
		k, v, ok := strings.Cut(s, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "label") {
			out = append(out, strings.Trim(strings.TrimSpace(v), "\"'"))
		}
	}
	for i, r := range body {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ',' && !inQuote:
			pair(body[start:i])
			start = i + 1
		}
	}
	pair(body[start:])
	return out
}

// decodeMermaidEntities turns mermaid's entity codes (#59; #quot; #9829;) into the characters a
// reader sees.
func decodeMermaidEntities(s string) string {
	return entityRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		if name[0] >= '0' && name[0] <= '9' {
			return html.UnescapeString("&#" + name + ";")
		}
		if d := html.UnescapeString("&" + name + ";"); d != "&"+name+";" && utf8.RuneCountInString(d) <= 2 {
			return d
		}
		return m
	})
}

// splitStatements splits a line at the semicolons outside double quotes: mermaid allows several
// statements on one line. A semicolon that ends an entity code (#59;) is not a separator.
func splitStatements(line string) []string {
	var out []string
	inQuote, start := false, 0
	for i, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ';' && !inQuote:
			if entityEndRe.MatchString(line[start:i]) {
				continue
			}
			out = append(out, line[start:i])
			start = i + 1
		}
	}
	return append(out, line[start:])
}
