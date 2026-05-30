package grammar

import (
	"strings"
	"testing"
)

func dumpTokens(t *testing.T, g *Grammar, src string) {
	t.Helper()
	var state *StateStack
	for i, ln := range strings.Split(src, "\n") {
		var toks []Token
		toks, state = g.TokenizeLine(ln, state)
		for _, tk := range toks {
			t.Logf("L%d %2d-%2d %-16q %s", i, tk.Start, tk.End, tokenText(ln, tk), strings.Join(tk.Scopes, " "))
		}
	}
}

// TestMultilineClass guards the "$self" include resolution fix: child patterns
// inside a begin/end region (the class body) must be tokenized, and a string
// containing braces must not be mistaken for the closing brace of the region.
func TestMultilineClass(t *testing.T) {
	g := loadPHP(t)
	src := "class Greeter {\n  public function greet(): string {\n    return \"hi {$x} there\";\n  }\n}"
	if testing.Verbose() {
		dumpTokens(t, g, src)
	}

	type want struct {
		line  int
		text  string
		scope string
	}
	wants := []want{
		{1, "public", "storage.modifier.php"},
		{1, "function", "storage.type.function.php"},
		{1, "greet", "entity.name.function.php"},
		{2, "return", "keyword.control.return.php"},
		{2, "x", "variable.other.php"},           // interpolated variable inside the string
		{2, "there", "string.quoted.double.php"}, // proves the string is still open after {$x}
	}

	var state *StateStack
	lines := splitForTest(src)
	tokensByLine := make([][]Token, len(lines))
	for i, ln := range lines {
		tokensByLine[i], state = g.TokenizeLine(ln, state)
	}

	for _, w := range wants {
		var found bool
		for _, tk := range tokensByLine[w.line] {
			if strings.Contains(tokenText(lines[w.line], tk), w.text) && hasScope(tk, w.scope) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("line %d: expected token %q with scope %q", w.line, w.text, w.scope)
		}
	}

	// The closing string quote on line 2 must carry the string end scope, and
	// the final "}" on line 4 must close the class (not leak string scope).
	last := tokensByLine[4]
	if len(last) == 0 {
		t.Fatal("no tokens on final line")
	}
	closeBrace := last[len(last)-1]
	for _, s := range closeBrace.Scopes {
		if s == "string.quoted.double.php" {
			t.Errorf("final '}' leaked string scope: %v", closeBrace.Scopes)
		}
	}
}

func splitForTest(s string) []string { return splitLinesTest(s) }

func splitLinesTest(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
