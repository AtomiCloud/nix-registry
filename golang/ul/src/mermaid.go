package main

import (
	"regexp"
	"strings"
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
		if label = strings.TrimSpace(label); label != "" {
			out = append(out, plainSeg(label, line))
		}
	}
	for i, raw := range lines {
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
		}
		if kind == "flow" && skipFlow.MatchString(t) {
			continue // styling and click directives hold no label
		}
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
			if m := participantRe.FindStringSubmatch(rest); m != nil {
				add(m[2], line)
			} else if m := noteRe.FindStringSubmatch(rest); m != nil {
				add(m[1], line)
			} else if m := messageRe.FindStringSubmatch(rest); m != nil {
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
	titleRe       = regexp.MustCompile(`(?i)^\s*(title|section|accTitle|accDescr)\s*:?\s+(.+)$`)
	skipFlow      = regexp.MustCompile(`(?i)^\s*(classDef|class|style|linkStyle|click|direction|end)\b`)
	subgraphRe    = regexp.MustCompile(`(?i)^\s*subgraph\s+([^\[\]]*?)\s*(\[.*)?$`)
	bracketRe     = regexp.MustCompile(`[\[({>]+([^\[\](){}"|>]*[^\[\](){}"|>\s/\\][^\[\](){}"|]*?)[\])}]+`)
	pipeRe        = regexp.MustCompile(`\|([^|"]+)\|`)
	participantRe = regexp.MustCompile(`(?i)^\s*(participant|actor)\s+\S+\s+as\s+(.+)$`)
	noteRe        = regexp.MustCompile(`(?i)^\s*note\s+(?:left of|right of|over)\s+[^:]+:\s*(.+)$`)
	messageRe     = regexp.MustCompile(`^\s*[^\s:]+\s*(?:-->>|->>|-->|->|--x|-x|--\)|-\))[+-]?\s*[^\s:]+\s*:\s*(.+)$`)
	stateEdgeRe   = regexp.MustCompile(`^\s*[^\s:]+\s*-->\s*[^\s:]+\s*:\s*(.+)$`)
	stateDescRe   = regexp.MustCompile(`^\s*[A-Za-z0-9_\-]+\s*:\s*(.+)$`)
	stateNoteRe   = regexp.MustCompile(`(?i)^\s*note\s+(?:left of|right of)\s+\S+\s*:\s*(.+)$`)
	inlineEdgeRes = []*regexp.Regexp{
		regexp.MustCompile(`--\s+([^->|]+?)\s+-->`),
		regexp.MustCompile(`-\.\s+([^.>|]+?)\s+\.->`),
		regexp.MustCompile(`==\s+([^=>|]+?)\s+==>`),
	}
)
