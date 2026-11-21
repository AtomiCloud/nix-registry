package main

import (
	"fmt"
	"slices"
)

// Translation modes: owner pick 4 of the doc system design (§11.7). Each mode is tested.
const (
	// modeFixed: every UL word has one fixed term per language in its `translations` field, and
	// "UL words translated" (UL501, UL502) holds each translated page to those terms.
	modeFixed = "fixed"
	// modeEnglish: UL words stay English in every translation. There is no `translations` field
	// (UL101 refuses it), and UL502 asks that a translation keeps the English word.
	modeEnglish = "english"
	// modeFree: the translator picks the term. There is no field and no check.
	modeFree = "free"
)

// defaultTranslations is the one-line switch for owner pick 4. It is the recommended option;
// --translations <mode> overrides it for one run.
const defaultTranslations = modeFixed

// defaultWhatFolder is the inner "what" folder of a bounded context (owner pick 2 of the doc
// system design); --what-folder <name> overrides it. "concept is a word" (UL408) reads it.
const defaultWhatFolder = "model"

var translationModes = []string{modeFixed, modeEnglish, modeFree}

// options is what one run takes from the flags.
type options struct {
	translations string
	whatFolder   string
	dh           string // the dh command; "" means $UL_DH, else "dh"
}

func (g *globals) options(getenv func(string) string) (options, error) {
	o := options{translations: g.translations, whatFolder: g.whatFolder, dh: g.dh}
	if o.translations == "" {
		o.translations = defaultTranslations
	}
	if o.whatFolder == "" {
		o.whatFolder = defaultWhatFolder
	}
	if !slices.Contains(translationModes, o.translations) {
		return o, usageErr{fmt.Sprintf("--translations %q: use one of fixed, english, free", o.translations)}
	}
	if o.dh == "" {
		o.dh = getenv("UL_DH")
	}
	if o.dh == "" {
		o.dh = "dh"
	}
	return o, nil
}
