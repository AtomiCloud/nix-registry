package main

import (
	"fmt"
	"path"
	"strings"
)

// "UL words translated" (design §11.7). For each translation under docs/i18n/, ul looks at the
// UL words its source page uses and holds the translation to them, by mode:
//
//	fixed    UL501: the word has no fixed term for the language in its translations field.
//	         UL502: the translation never uses the fixed term.
//	english  UL502: the translation does not keep the English word the source used.
//	free     nothing (the translator picks the term; pages under docs/i18n/ are not read).
func (l *loaded) translationProblems(f *Final, pages []*page, uses map[string]*pageUse) ([]Problem, error) {
	if l.opts.translations == modeFree {
		return nil, nil
	}
	byRel := map[string]*page{}
	for _, p := range pages {
		byRel[p.rel] = p
	}
	var probs []Problem
	for _, t := range pages {
		if !t.i18n || t.tr == nil || t.tr.lang == "" || t.tr.source == "" {
			continue // doccheck's "front matter valid" refuses a translation without lang and source
		}
		srcRel := path.Clean(path.Join(path.Dir(t.rel), t.tr.source))
		use := uses[srcRel]
		if use == nil || !strings.HasSuffix(srcRel, ".md") {
			probs = append(probs, Problem{Code: "UL503", File: t.rel, Line: max(t.tr.line, 1),
				Message: fmt.Sprintf("the source %s is not a page under docs/ with a namespaces list", t.tr.source)})
			continue
		}
		var sb strings.Builder
		for _, s := range t.segs {
			sb.WriteString(s.Text)
			sb.WriteByte('\n')
		}
		text := strings.ToLower(sb.String())
		for _, u := range use.entries {
			switch l.opts.translations {
			case modeFixed:
				term := ""
				for _, tr := range u.fe.E.Translations {
					if strings.EqualFold(tr.Lang, t.tr.lang) {
						term = tr.Term
					}
				}
				switch {
				case term == "":
					probs = append(probs, Problem{Code: "UL501", File: t.rel, Line: max(t.tr.line, 1),
						Message: fmt.Sprintf("the source uses UL word %q (%s), which has no %s term in its translations field", u.fe.E.Word, u.fe.NS.Namespace, t.tr.lang)})
				case !hasTerm(text, strings.ToLower(term)):
					probs = append(probs, Problem{Code: "UL502", File: t.rel, Line: max(t.tr.line, 1),
						Message: fmt.Sprintf("the source uses UL word %q, but its fixed %s term %q never appears", u.fe.E.Word, t.tr.lang, term)})
				}
			case modeEnglish:
				kept := false
				for n := range u.names {
					if hasEnglishName(text, n, u.fe) {
						kept = true
						break
					}
				}
				if !kept {
					probs = append(probs, Problem{Code: "UL502", File: t.rel, Line: max(t.tr.line, 1),
						Message: fmt.Sprintf("the source uses UL word %q, but the translation never keeps its English word", u.fe.E.Word)})
				}
			}
		}
	}
	return probs, nil
}

// hasTerm reports whether text holds term as a word of its own (not inside another word), or with
// a plural ending: -s or -es.
func hasTerm(text, term string) bool {
	if term == "" {
		return false
	}
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], term)
		if j < 0 {
			return false
		}
		a, b := i+j, i+j+len(term)
		if a == 0 || !isWordRune(lastRune(text[:a])) {
			if b >= len(text) || !isWordRune(firstRune(text[b:])) {
				return true
			}
			for _, end := range []string{"s", "es"} {
				if strings.HasPrefix(text[b:], end) && (b+len(end) >= len(text) || !isWordRune(firstRune(text[b+len(end):]))) {
					return true
				}
			}
		}
		i = a + 1
	}
	return false
}

// hasEnglishName reports whether text keeps the English name the source used, as a word of its own
// and with the plural endings English writes (the same rule that found the name in the source).
func hasEnglishName(text, name string, fe *FEntry) bool {
	t := &trie{}
	t.insert(name, fe)
	return len(t.scanSpans(text)) > 0
}
