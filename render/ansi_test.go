package render

import (
	"strings"
	"testing"

	"github.com/andersonpem/gopher-textmate/grammar"
	"github.com/andersonpem/gopher-textmate/theme"
)

func testTheme(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.Parse([]byte(`{
      "colors": {"editor.foreground": "#d4d4d4"},
      "tokenColors": [
        {"settings": {"foreground": "#d4d4d4"}},
        {"scope": "keyword", "settings": {"foreground": "#ff0000", "fontStyle": "bold"}}
      ]
    }`))
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestRenderTrueColor(t *testing.T) {
	th := testTheme(t)
	line := "if x"
	tokens := []grammar.Token{
		{Start: 0, End: 2, Scopes: []string{"source", "keyword.control"}},
		{Start: 2, End: 4, Scopes: []string{"source"}},
	}
	out := RenderLine(line, tokens, th, Options{Mode: TrueColor})
	if !strings.Contains(out, "\x1b[1;38;2;255;0;0mif\x1b[0m") {
		t.Errorf("expected bold red 'if', got %q", out)
	}
	if !strings.HasSuffix(stripReset(out), "x") {
		// the trailing token text must be present
		t.Errorf("missing trailing text in %q", out)
	}
}

func stripReset(s string) string {
	return strings.TrimSuffix(s, reset)
}

func TestRenderNoneMode(t *testing.T) {
	th := testTheme(t)
	line := "if x"
	tokens := []grammar.Token{{Start: 0, End: 4, Scopes: []string{"source", "keyword"}}}
	out := RenderLine(line, tokens, th, Options{Mode: None})
	if out != "if x" {
		t.Errorf("None mode should be plain text, got %q", out)
	}
}

func TestRender256(t *testing.T) {
	th := testTheme(t)
	tokens := []grammar.Token{{Start: 0, End: 2, Scopes: []string{"keyword"}}}
	out := RenderLine("if", tokens, th, Options{Mode: Color256})
	if !strings.Contains(out, "38;5;") {
		t.Errorf("expected 256-color escape, got %q", out)
	}
}

func TestTo256PureColors(t *testing.T) {
	if got := to256(theme.RGB{R: 255, G: 0, B: 0}); got != 196 {
		t.Errorf("pure red -> %d, want 196", got)
	}
	if got := to256(theme.RGB{R: 0, G: 0, B: 0}); got != 16 {
		t.Errorf("black -> %d, want 16", got)
	}
	if got := to256(theme.RGB{R: 255, G: 255, B: 255}); got != 231 {
		t.Errorf("white -> %d, want 231", got)
	}
}
