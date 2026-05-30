package grammar

import (
	"strings"
	"testing"
)

func loadPHP(t *testing.T) *Grammar {
	t.Helper()
	reg := NewRegistry()
	if _, err := reg.LoadGrammarFile("../grammars/php.tmLanguage.json"); err != nil {
		t.Fatalf("load php grammar: %v", err)
	}
	g, err := reg.Grammar("source.php")
	if err != nil {
		t.Fatalf("compile php grammar: %v", err)
	}
	return g
}

// scopesAt returns the scope list covering the first rune of the first token
// whose text equals want, or nil.
func tokenText(line string, tok Token) string {
	r := []rune(line)
	if tok.Start < 0 || tok.End > len(r) {
		return ""
	}
	return string(r[tok.Start:tok.End])
}

// findScope returns the innermost scope of the first token whose text equals
// want, or "" if not found.
func findToken(t *testing.T, g *Grammar, line, want string) Token {
	t.Helper()
	toks, _ := g.TokenizeLine(line, nil)
	for _, tk := range toks {
		if tokenText(line, tk) == want {
			return tk
		}
	}
	return Token{Start: -1}
}

func hasScope(tk Token, scope string) bool {
	for _, s := range tk.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func TestPHPFunctionDeclaration(t *testing.T) {
	g := loadPHP(t)
	line := `function foo($bar) { return 1; }`
	toks, _ := g.TokenizeLine(line, nil)
	if len(toks) == 0 {
		t.Fatal("no tokens produced")
	}
	for _, tk := range toks {
		t.Logf("%2d-%2d %-12q %s", tk.Start, tk.End, tokenText(line, tk), strings.Join(tk.Scopes, " "))
	}

	cases := []struct {
		text, scope string
	}{
		{"function", "storage.type.function.php"},
		{"foo", "entity.name.function.php"},
		{"bar", "variable.other.php"},
	}
	for _, c := range cases {
		tk := findToken(t, g, line, c.text)
		if tk.Start < 0 {
			t.Errorf("token %q not found", c.text)
			continue
		}
		if !hasScope(tk, c.scope) {
			t.Errorf("token %q: expected scope %q, got %v", c.text, c.scope, tk.Scopes)
		}
		if tk.Scopes[0] != "source.php" {
			t.Errorf("token %q: expected base scope source.php, got %v", c.text, tk.Scopes)
		}
	}
}

func TestPHPControlKeyword(t *testing.T) {
	g := loadPHP(t)
	line := `return $x;`
	tk := findToken(t, g, line, "return")
	if tk.Start < 0 {
		t.Fatal("'return' token not found")
	}
	if !hasScope(tk, "keyword.control.return.php") {
		t.Errorf("expected keyword.control.return.php for 'return', got %v", tk.Scopes)
	}
}

func TestPHPString(t *testing.T) {
	g := loadPHP(t)
	line := `$name = "hello world";`
	toks, _ := g.TokenizeLine(line, nil)
	var stringScoped bool
	for _, tk := range toks {
		if tokenText(line, tk) == "hello world" {
			for _, s := range tk.Scopes {
				if strings.HasPrefix(s, "string.quoted.double") {
					stringScoped = true
				}
			}
		}
	}
	if !stringScoped {
		t.Errorf("expected the string body to carry a string.quoted.double scope")
		for _, tk := range toks {
			t.Logf("%2d-%2d %-14q %s", tk.Start, tk.End, tokenText(line, tk), strings.Join(tk.Scopes, " "))
		}
	}
}
