package gopher_textmate_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	textmate "github.com/andersonpem/gopher-textmate"
	"github.com/andersonpem/gopher-textmate/render"
)

// Example demonstrates the minimal end-to-end use of the library: load a
// grammar, set a theme, and tokenize source into scoped tokens.
func Example() {
	h, err := textmate.New(textmate.WithColorMode(render.None))
	if err != nil {
		panic(err)
	}
	if _, err := h.LoadGrammarFile("grammars/php.tmLanguage.json"); err != nil {
		panic(err)
	}
	if err := h.SetThemeBytes(textmate.DefaultThemeBytes()); err != nil {
		panic(err)
	}

	lines, err := h.Tokenize("source.php", `return 1;`)
	if err != nil {
		panic(err)
	}
	for _, tok := range lines[0] {
		if hasScopePrefix(tok.Scopes, "keyword.control") {
			fmt.Printf("keyword at %d-%d\n", tok.Start, tok.End)
		}
	}
	// Output: keyword at 0-6
}

func hasScopePrefix(scopes []string, prefix string) bool {
	for _, s := range scopes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func newHighlighter(t *testing.T) *textmate.Highlighter {
	t.Helper()
	h, err := textmate.New(textmate.WithColorMode(render.TrueColor))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.LoadGrammarFile("grammars/php.tmLanguage.json"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetThemeBytes(textmate.DefaultThemeBytes()); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHighlightProducesANSI(t *testing.T) {
	h := newHighlighter(t)
	out, err := h.Highlight("source.php", "function foo() {}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI escapes in output, got %q", out)
	}
}

func TestDefaultScope(t *testing.T) {
	h := newHighlighter(t)
	// No scope argument: should fall back to the first grammar loaded.
	lines, err := h.Tokenize("", "return 1;")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0]) == 0 {
		t.Fatalf("expected tokens for one line, got %v", lines)
	}
}

func TestBundledGrammars(t *testing.T) {
	h, _ := textmate.New(textmate.WithColorMode(render.None))
	if err := h.LoadBundledGrammars(); err != nil {
		t.Fatal(err)
	}
	if !h.Registry().HasGrammar("source.php") {
		t.Error("expected bundled source.php grammar to be available")
	}
}

func sameTokens(a, b []textmate.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			ta, tb := a[i][j], b[i][j]
			if ta.Start != tb.Start || ta.End != tb.End || strings.Join(ta.Scopes, "|") != strings.Join(tb.Scopes, "|") {
				return false
			}
		}
	}
	return true
}

// TestDocumentIncrementalMatchesFull verifies that incremental re-tokenization
// produces exactly the same tokens as a from-scratch tokenization, both for the
// initial text and after editing a line in the middle.
func TestDocumentIncrementalMatchesFull(t *testing.T) {
	h := newHighlighter(t)
	doc, err := h.NewDocument("source.php")
	if err != nil {
		t.Fatal(err)
	}

	text := "class A {\n    public function f(): int {\n        return 1;\n    }\n}"
	doc.SetText(text)
	full, _ := h.Tokenize("source.php", text)
	got := docTokens(doc)
	if !sameTokens(got, full) {
		t.Fatal("initial incremental tokens differ from full tokenization")
	}

	// Edit a middle line.
	edited := "class A {\n    public function f(): string {\n        return \"x\";\n    }\n}"
	doc.SetText(edited)
	fullEdited, _ := h.Tokenize("source.php", edited)
	if !sameTokens(docTokens(doc), fullEdited) {
		t.Fatal("post-edit incremental tokens differ from full tokenization")
	}
}

func docTokens(d *textmate.Document) []textmate.Line {
	out := make([]textmate.Line, d.Len())
	for i := 0; i < d.Len(); i++ {
		out[i] = d.Line(i)
	}
	return out
}

// TestDocumentReusesUnchangedLines verifies the incremental fast path: editing
// the last line of a buffer reports only that line as changed.
func TestDocumentReusesUnchangedLines(t *testing.T) {
	h := newHighlighter(t)
	doc, _ := h.NewDocument("source.php")
	doc.SetText("$a = 1;\n$b = 2;\n$c = 3;")
	changed := doc.SetLine(2, "$c = 4;")
	if len(changed) != 1 || changed[0] != 2 {
		t.Errorf("expected only line 2 to change, got %v", changed)
	}
}

// TestConcurrentTokenize verifies that a Highlighter is safe to use from
// multiple goroutines after setup (run with -race).
func TestConcurrentTokenize(t *testing.T) {
	h := newHighlighter(t)
	// Warm up: compile the grammar once before sharing across goroutines.
	if _, err := h.Tokenize("source.php", "warmup;"); err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	src := "class A { public function f(): int { return $x + 1; } }"
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := h.Tokenize("source.php", src); err != nil {
					t.Errorf("tokenize: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
