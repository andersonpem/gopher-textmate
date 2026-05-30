package gopher_textmate

import (
	"io"
	"strings"

	"github.com/andersonpem/gopher-textmate/grammar"
	"github.com/andersonpem/gopher-textmate/render"
)

// Document is an incrementally re-tokenized buffer, intended for interactive
// editors and REPLs that repaint on every keystroke. It caches each line's
// tokens together with the tokenizer state that produced it; on SetText it only
// re-tokenizes the lines that actually changed, reusing the unchanged prefix
// and stopping as soon as the tokenizer state reconverges with the previous
// result (the unchanged suffix is reused wholesale).
//
// A Document is not safe for concurrent use; confine it to a single goroutine
// (e.g. the UI loop).
type Document struct {
	h     *Highlighter
	g     *grammar.Grammar
	scope string
	lines []docLine
}

type docLine struct {
	text   string
	start  *grammar.StateStack
	end    *grammar.StateStack
	tokens []grammar.Token
}

// NewDocument creates an incremental document tokenized with the given grammar
// scope (empty uses the highlighter's default scope).
func (h *Highlighter) NewDocument(scope string) (*Document, error) {
	g, err := h.grammarFor(scope)
	if err != nil {
		return nil, err
	}
	return &Document{h: h, g: g, scope: scope}, nil
}

// SetText replaces the document contents, re-tokenizing only what changed.
// It returns the indices of the lines whose tokens changed (useful for partial
// repaints); the slice is nil when nothing changed.
func (d *Document) SetText(text string) []int {
	newTexts := splitLines(text)
	old := d.lines
	newLines := make([]docLine, len(newTexts))
	var changed []int

	var prevEnd *grammar.StateStack
	for i, txt := range newTexts {
		start := prevEnd
		if i < len(old) && old[i].text == txt && old[i].start.Equals(start) {
			// Unchanged text and identical start state: reuse cached tokens and
			// end state. This also lets the entire unchanged tail cascade-reuse.
			newLines[i] = old[i]
			prevEnd = old[i].end
			continue
		}
		toks, end := d.g.TokenizeLine(txt, start)
		newLines[i] = docLine{text: txt, start: start, end: end, tokens: toks}
		prevEnd = end
		changed = append(changed, i)
	}

	d.lines = newLines
	return changed
}

// SetLine replaces a single line's text and re-tokenizes from there until the
// state reconverges. Returns the changed line indices. Out-of-range indices are
// ignored.
func (d *Document) SetLine(i int, text string) []int {
	if i < 0 || i >= len(d.lines) {
		return nil
	}
	texts := make([]string, len(d.lines))
	for k := range d.lines {
		texts[k] = d.lines[k].text
	}
	texts[i] = text
	return d.SetText(strings.Join(texts, "\n"))
}

// Len returns the number of lines.
func (d *Document) Len() int { return len(d.lines) }

// Line returns the tokens for line i (nil if out of range). The returned slice
// must not be mutated.
func (d *Document) Line(i int) []grammar.Token {
	if i < 0 || i >= len(d.lines) {
		return nil
	}
	return d.lines[i].tokens
}

// LineText returns the source text of line i.
func (d *Document) LineText(i int) string {
	if i < 0 || i >= len(d.lines) {
		return ""
	}
	return d.lines[i].text
}

// RenderLine renders line i to an ANSI string using the highlighter's theme and
// color mode.
func (d *Document) RenderLine(i int) string {
	if i < 0 || i >= len(d.lines) {
		return ""
	}
	opts := render.Options{Mode: d.h.mode, Background: d.h.background}
	return render.RenderLine(d.lines[i].text, d.lines[i].tokens, d.h.theme, opts)
}

// Render renders the whole document to an ANSI string (lines joined by "\n").
func (d *Document) Render() string {
	var b strings.Builder
	_ = d.RenderTo(&b)
	return b.String()
}

// RenderTo writes the whole document's ANSI rendering to w.
func (d *Document) RenderTo(w io.Writer) error {
	opts := render.Options{Mode: d.h.mode, Background: d.h.background}
	for i := range d.lines {
		if _, err := io.WriteString(w, render.RenderLine(d.lines[i].text, d.lines[i].tokens, d.h.theme, opts)); err != nil {
			return err
		}
		if i < len(d.lines)-1 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}
