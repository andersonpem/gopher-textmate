package gopher_textmate_test

import (
	"os"
	"strings"
	"testing"

	textmate "github.com/andersonpem/gopher-textmate"
	"github.com/andersonpem/gopher-textmate/render"
)

func benchHighlighter(b *testing.B) (*textmate.Highlighter, string) {
	b.Helper()
	h, err := textmate.New(textmate.WithColorMode(render.None))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := h.LoadGrammarFile("grammars/php.tmLanguage.json"); err != nil {
		b.Fatal(err)
	}
	if err := h.SetThemeBytes(textmate.DefaultThemeBytes()); err != nil {
		b.Fatal(err)
	}
	data, err := os.ReadFile("examples/complicated.php")
	if err != nil {
		b.Fatal(err)
	}
	return h, string(data)
}

// BenchmarkTokenizeFullBuffer tokenizes the entire ~500-line file once per op.
// It warms up first so the measured cost is steady-state matching, not the
// one-time lazy compilation of all patterns.
func BenchmarkTokenizeFullBuffer(b *testing.B) {
	h, src := benchHighlighter(b)
	if _, err := h.Tokenize("source.php", src); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Tokenize("source.php", src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkColdCompile measures the one-time cost of compiling all patterns and
// tokenizing the buffer for the first time (REPL/editor startup cost).
func BenchmarkColdCompile(b *testing.B) {
	_, src := benchHighlighter(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		h, _ := benchHighlighter(b)
		b.StartTimer()
		if _, err := h.Tokenize("source.php", src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTokenizeSingleLine measures re-tokenizing one representative line
// from a clean start state (the per-keystroke cost when not incremental).
func BenchmarkTokenizeSingleLine(b *testing.B) {
	h, _ := benchHighlighter(b)
	line := `        return "{$this->prefix}, {$user->name}! You have {$count} messages.";`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := h.TokenizeLine("source.php", line, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWarmup measures the parallel one-time pattern compilation cost.
func BenchmarkWarmup(b *testing.B) {
	_, src := benchHighlighter(b)
	_ = src
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		h, _ := benchHighlighter(b)
		b.StartTimer()
		if err := h.Warmup("source.php"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDocumentKeystroke simulates the per-keystroke cost in a REPL: a
// multi-line buffer where the user is typing on the last line. Only the edited
// line (and any lines until state reconverges) is re-tokenized.
func BenchmarkDocumentKeystroke(b *testing.B) {
	h, src := benchHighlighter(b)
	_ = h.Warmup("source.php")
	doc, err := h.NewDocument("source.php")
	if err != nil {
		b.Fatal(err)
	}
	doc.SetText(src)
	last := doc.Len() - 1
	base := doc.LineText(last)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate typing one more character on the last line.
		doc.SetLine(last, base+"x")
	}
}

// BenchmarkDocumentFullVsIncremental contrasts re-tokenizing the whole buffer
// on each edit (SetText of the full text after a single-line change).
func BenchmarkDocumentReplaceLastLine(b *testing.B) {
	h, src := benchHighlighter(b)
	_ = h.Warmup("source.php")
	doc, _ := h.NewDocument("source.php")
	doc.SetText(src)
	last := doc.Len() - 1
	base := doc.LineText(last)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.SetLine(last, base+"y")
	}
}

var _ = strings.Count
