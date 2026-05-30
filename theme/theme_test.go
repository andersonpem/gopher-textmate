package theme

import "testing"

const sampleTheme = `{
  "name": "Test",
  "colors": { "editor.foreground": "#d4d4d4", "editor.background": "#1e1e1e" },
  "tokenColors": [
    { "settings": { "foreground": "#d4d4d4" } },
    { "scope": "comment", "settings": { "foreground": "#6a9955", "fontStyle": "italic" } },
    { "scope": ["keyword", "storage.type"], "settings": { "foreground": "#569cd6" } },
    { "scope": "string.quoted.double", "settings": { "foreground": "#ce9178" } },
    { "scope": "meta.function entity.name.function", "settings": { "foreground": "#dcdcaa", "fontStyle": "bold" } }
  ]
}`

func parse(t *testing.T) *Theme {
	t.Helper()
	th, err := Parse([]byte(sampleTheme))
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func eqColor(c *RGB, r, g, b uint8) bool {
	return c != nil && c.R == r && c.G == g && c.B == b
}

func TestDefaults(t *testing.T) {
	th := parse(t)
	if !eqColor(th.DefaultFG, 0xd4, 0xd4, 0xd4) {
		t.Errorf("default fg = %+v", th.DefaultFG)
	}
	if !eqColor(th.DefaultBG, 0x1e, 0x1e, 0x1e) {
		t.Errorf("default bg = %+v", th.DefaultBG)
	}
}

func TestPrefixMatch(t *testing.T) {
	th := parse(t)
	// "string.quoted.double.php" should match the "string.quoted.double" rule.
	st := th.Match([]string{"source.php", "string.quoted.double.php"})
	if !eqColor(st.Foreground, 0xce, 0x91, 0x78) {
		t.Errorf("expected string color, got %+v", st.Foreground)
	}
}

func TestFontStyle(t *testing.T) {
	th := parse(t)
	st := th.Match([]string{"source.php", "comment.block.php"})
	if !st.Italic {
		t.Error("expected comment to be italic")
	}
}

func TestDescendantSelectorMoreSpecific(t *testing.T) {
	th := parse(t)
	// entity.name.function alone has no rule, but "meta.function entity.name.function" does.
	st := th.Match([]string{"source.php", "meta.function.php", "entity.name.function.php"})
	if !eqColor(st.Foreground, 0xdc, 0xdc, 0xaa) {
		t.Errorf("expected function color via descendant selector, got %+v", st.Foreground)
	}
	if !st.Bold {
		t.Error("expected function rule to be bold")
	}
}

func TestKeywordArrayScope(t *testing.T) {
	th := parse(t)
	st := th.Match([]string{"source.php", "keyword.control.return.php"})
	if !eqColor(st.Foreground, 0x56, 0x9c, 0xd6) {
		t.Errorf("expected keyword color, got %+v", st.Foreground)
	}
}

func TestShortHexColor(t *testing.T) {
	c := parseColor("#abc")
	if !eqColor(c, 0xaa, 0xbb, 0xcc) {
		t.Errorf("short hex parse = %+v", c)
	}
}
