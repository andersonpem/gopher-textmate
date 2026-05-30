package gopher_textmate

import (
	"embed"

	"github.com/andersonpem/gopher-textmate/theme"
)

//go:embed themes/dark.json
var defaultThemeJSON []byte

//go:embed grammars/*.json
var bundledGrammars embed.FS

// DefaultThemeBytes returns the JSON bytes of the bundled "Gopher Dark" theme.
func DefaultThemeBytes() []byte {
	out := make([]byte, len(defaultThemeJSON))
	copy(out, defaultThemeJSON)
	return out
}

// DefaultTheme parses and returns the bundled dark theme.
func DefaultTheme() (*theme.Theme, error) {
	return theme.Parse(defaultThemeJSON)
}

// BundledGrammars exposes the grammars shipped with this module as a read-only
// filesystem (files live under "grammars/").
func BundledGrammars() embed.FS { return bundledGrammars }

// LoadBundledGrammars loads every grammar embedded in the module into the
// highlighter's registry.
func (h *Highlighter) LoadBundledGrammars() error {
	return h.LoadGrammarFS(bundledGrammars, "grammars/*.json")
}
