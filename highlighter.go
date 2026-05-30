// Package textmate is a pure-Go (no cgo) TextMate grammar interpreter for
// syntax highlighting. It tokenizes source text into scoped tokens using
// TextMate grammars and can resolve a colour theme to produce ANSI output for
// terminals.
//
// The package is designed to be embedded in other applications. The Highlighter
// type is the high-level facade; the underlying grammar, theme and render
// packages are also exported for callers that need finer control.
//
// A Highlighter is safe for concurrent tokenization once all grammars and the
// theme have been configured (the setup-then-use contract): perform all
// LoadGrammar*/SetTheme* calls before sharing it across goroutines.
package gopher_textmate

import (
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/andersonpem/gopher-textmate/grammar"
	"github.com/andersonpem/gopher-textmate/render"
	"github.com/andersonpem/gopher-textmate/theme"
)

// Line is the list of tokens produced for a single line of input.
type Line = []grammar.Token

// Highlighter ties together a grammar registry, a theme and rendering options.
// Construct one with New.
type Highlighter struct {
	registry     *grammar.Registry
	theme        *theme.Theme
	mode         render.ColorMode
	background   bool
	defaultScope string
}

// Option configures a Highlighter.
type Option func(*Highlighter)

// WithColorMode sets the ANSI colour mode (defaults to render.DetectColorMode).
func WithColorMode(m render.ColorMode) Option {
	return func(h *Highlighter) { h.mode = m }
}

// WithBackground enables emitting token background colours.
func WithBackground(on bool) Option {
	return func(h *Highlighter) { h.background = on }
}

// WithDefaultScope sets the grammar scope used when an empty scope is passed to
// Tokenize/Highlight.
func WithDefaultScope(scope string) Option {
	return func(h *Highlighter) { h.defaultScope = scope }
}

// New creates a Highlighter. By default it has an empty grammar registry, no
// theme, and an auto-detected colour mode.
func New(opts ...Option) (*Highlighter, error) {
	h := &Highlighter{
		registry: grammar.NewRegistry(),
		mode:     render.DetectColorMode(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// Registry exposes the underlying grammar registry for advanced use.
func (h *Highlighter) Registry() *grammar.Registry { return h.registry }

// Theme returns the currently configured theme (may be nil).
func (h *Highlighter) Theme() *theme.Theme { return h.theme }

// ColorMode returns the active colour mode.
func (h *Highlighter) ColorMode() render.ColorMode { return h.mode }

// LoadGrammarFile loads a grammar from a .tmLanguage.json file and returns its
// scope name. The first grammar loaded becomes the default scope unless one was
// already set via WithDefaultScope.
func (h *Highlighter) LoadGrammarFile(path string) (string, error) {
	scope, err := h.registry.LoadGrammarFile(path)
	if err != nil {
		return "", err
	}
	h.maybeSetDefaultScope(scope)
	return scope, nil
}

// LoadGrammarBytes loads a grammar from raw tmLanguage JSON bytes.
func (h *Highlighter) LoadGrammarBytes(data []byte) (string, error) {
	scope, err := h.registry.AddGrammarBytes(data)
	if err != nil {
		return "", err
	}
	h.maybeSetDefaultScope(scope)
	return scope, nil
}

// LoadGrammarFS loads every grammar file in fsys matching glob (e.g. "*.json").
func (h *Highlighter) LoadGrammarFS(fsys fs.FS, glob string) error {
	matches, err := fs.Glob(fsys, glob)
	if err != nil {
		return err
	}
	for _, m := range matches {
		data, err := fs.ReadFile(fsys, m)
		if err != nil {
			return fmt.Errorf("textmate: read %s: %w", m, err)
		}
		if _, err := h.LoadGrammarBytes(data); err != nil {
			return fmt.Errorf("textmate: load %s: %w", m, err)
		}
	}
	return nil
}

func (h *Highlighter) maybeSetDefaultScope(scope string) {
	if h.defaultScope == "" {
		h.defaultScope = scope
	}
}

// SetTheme sets an already-parsed theme.
func (h *Highlighter) SetTheme(t *theme.Theme) { h.theme = t }

// SetThemeFile loads and sets a VSCode-style JSON theme from a file.
func (h *Highlighter) SetThemeFile(path string) error {
	t, err := theme.ParseFile(path)
	if err != nil {
		return err
	}
	h.theme = t
	return nil
}

// SetThemeBytes loads and sets a VSCode-style JSON theme from bytes.
func (h *Highlighter) SetThemeBytes(data []byte) error {
	t, err := theme.Parse(data)
	if err != nil {
		return err
	}
	h.theme = t
	return nil
}

// Warmup compiles the grammar for scope and eagerly compiles all of its
// patterns (in parallel) so the one-time regex compilation cost does not land
// on the latency-sensitive tokenization path. Call it once at startup; for a
// responsive UI, run it in a background goroutine:
//
//	go h.Warmup("source.php")
func (h *Highlighter) Warmup(scope string) error {
	if _, err := h.grammarFor(scope); err != nil {
		return err
	}
	h.registry.Warmup()
	return nil
}

func (h *Highlighter) grammarFor(scope string) (*grammar.Grammar, error) {
	if scope == "" {
		scope = h.defaultScope
	}
	if scope == "" {
		return nil, fmt.Errorf("textmate: no scope specified and no default scope set")
	}
	return h.registry.Grammar(scope)
}

// TokenizeLine tokenizes a single line (which must not contain newlines),
// carrying tokenizer state via prev (nil for the first line). It returns the
// tokens and the state to pass to the next line.
func (h *Highlighter) TokenizeLine(scope, line string, prev *grammar.StateStack) (Line, *grammar.StateStack, error) {
	g, err := h.grammarFor(scope)
	if err != nil {
		return nil, nil, err
	}
	toks, next := g.TokenizeLine(line, prev)
	return toks, next, nil
}

// Tokenize splits source into lines and tokenizes each, returning one Line per
// input line.
func (h *Highlighter) Tokenize(scope, source string) ([]Line, error) {
	g, err := h.grammarFor(scope)
	if err != nil {
		return nil, err
	}
	lines := splitLines(source)
	out := make([]Line, len(lines))
	var state *grammar.StateStack
	for i, ln := range lines {
		out[i], state = g.TokenizeLine(ln, state)
	}
	return out, nil
}

// Highlight tokenizes source and renders it to a single ANSI string.
func (h *Highlighter) Highlight(scope, source string) (string, error) {
	var b strings.Builder
	if err := h.HighlightTo(&b, scope, source); err != nil {
		return "", err
	}
	return b.String(), nil
}

// HighlightTo tokenizes source and writes ANSI-highlighted output to w.
func (h *Highlighter) HighlightTo(w io.Writer, scope, source string) error {
	if h.theme == nil && h.mode != render.None {
		return fmt.Errorf("textmate: no theme set (call SetTheme* or use WithColorMode(render.None))")
	}
	g, err := h.grammarFor(scope)
	if err != nil {
		return err
	}
	opts := render.Options{Mode: h.mode, Background: h.background}
	lines := splitLines(source)
	var state *grammar.StateStack
	for i, ln := range lines {
		var toks Line
		toks, state = g.TokenizeLine(ln, state)
		if _, err := io.WriteString(w, render.RenderLine(ln, toks, h.theme, opts)); err != nil {
			return err
		}
		if i < len(lines)-1 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// splitLines splits text on "\n" without dropping a trailing empty segment,
// and strips a trailing "\r" from each line (CRLF tolerance).
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSuffix(ln, "\r")
	}
	return lines
}
