package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
)

// The JSON contract ul/v1 (spec §6).

type JNamespace struct {
	Namespace  string   `json:"namespace"`
	Breadcrumb []string `json:"breadcrumb"`
	Premise    string   `json:"premise"`
	File       string   `json:"file"`
	Layer      string   `json:"layer"`
}

type JWord struct {
	ID         string   `json:"id"`
	Word       string   `json:"word"`
	Namespace  string   `json:"namespace"`
	Breadcrumb []string `json:"breadcrumb"`
	Aliases    []string `json:"aliases"`
	Desc       string   `json:"desc"`
	Details    string   `json:"details"`
	Avoid      []string `json:"avoid"`
	See        []string `json:"see"`
	Mentions   []string `json:"mentions"`
	Status     string   `json:"status"`
	ReplacedBy *string  `json:"replacedBy"`
	Layer      string   `json:"layer"`
	Overrides  *string  `json:"overrides"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
}

type Report struct {
	Schema     string       `json:"schema"`
	Root       string       `json:"root"`
	Session    *string      `json:"session"`
	Namespaces []JNamespace `json:"namespaces"`
	Words      []JWord      `json:"words"`
	Problems   []Problem    `json:"problems"`
}

func names(ns []Named) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func (fe *FEntry) jword() JWord {
	w := JWord{
		ID: fe.ID, Word: fe.E.Word, Namespace: fe.NS.Namespace, Breadcrumb: fe.NS.Breadcrumb,
		Aliases: names(fe.E.Aliases), Desc: fe.E.Desc, Details: fe.E.Details, Avoid: names(fe.E.Avoid),
		See: nonNil(fe.See), Mentions: nonNil(fe.Mentions), Status: fe.E.Status,
		Layer: fe.Layer, File: fe.File, Line: fe.E.WordLine,
	}
	if fe.ReplacedBy != "" {
		s := fe.ReplacedBy
		w.ReplacedBy = &s
	}
	if fe.Overrides {
		s := layerRepo
		w.Overrides = &s
	}
	return w
}

// relFile shows a repo path relative to the root (already so) and a session path as is.
func relFile(f string) string { return filepath.ToSlash(f) }

func (f *Final) report() *Report {
	r := &Report{Schema: "ul/v1", Root: f.Root, Session: f.Session,
		Namespaces: make([]JNamespace, 0, len(f.NS)), Words: make([]JWord, 0, len(f.Entries)), Problems: slices.Clone(f.Problems)}
	if r.Problems == nil {
		r.Problems = []Problem{}
	}
	for _, ns := range f.NS {
		r.Namespaces = append(r.Namespaces, JNamespace{ns.Namespace, ns.Breadcrumb, ns.Premise, relFile(ns.File), ns.Layer})
	}
	for _, fe := range f.Entries {
		r.Words = append(r.Words, fe.jword())
	}
	return r
}

func writeJSON(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
