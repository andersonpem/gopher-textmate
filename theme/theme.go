// Package theme parses VSCode-style JSON colour themes and resolves the visual
// style (foreground/background colour and font style) for a token's scope
// stack, using TextMate scope-selector matching semantics.
package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// Style is the resolved appearance for a token.
type Style struct {
	Foreground *RGB
	Background *RGB
	Bold       bool
	Italic     bool
	Underline  bool
}

// Theme is a parsed colour theme.
type Theme struct {
	Name      string
	DefaultFG *RGB
	DefaultBG *RGB
	rules     []themeRule
}

type themeRule struct {
	selectors [][]string // each selector is a list of descendant segments (outer..inner)
	fg        *RGB
	bg        *RGB
	font      *fontStyle // nil when the rule does not set fontStyle
	order     int
}

type fontStyle struct {
	bold      bool
	italic    bool
	underline bool
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

type rawTheme struct {
	Name        string            `json:"name"`
	Colors      map[string]string `json:"colors"`
	TokenColors []rawTokenColor   `json:"tokenColors"`
	Settings    []rawTokenColor   `json:"settings"` // tmTheme-converted themes
}

type rawTokenColor struct {
	Scope    json.RawMessage `json:"scope"`
	Settings rawSettings     `json:"settings"`
}

type rawSettings struct {
	Foreground string  `json:"foreground"`
	Background string  `json:"background"`
	FontStyle  *string `json:"fontStyle"`
}

// Parse decodes a theme from VSCode-style JSON bytes.
func Parse(data []byte) (*Theme, error) {
	var rt rawTheme
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, fmt.Errorf("theme: parse: %w", err)
	}
	t := &Theme{Name: rt.Name}

	if fg, ok := rt.Colors["editor.foreground"]; ok {
		t.DefaultFG = parseColor(fg)
	}
	if bg, ok := rt.Colors["editor.background"]; ok {
		t.DefaultBG = parseColor(bg)
	}

	entries := rt.TokenColors
	if len(entries) == 0 {
		entries = rt.Settings
	}
	for i, e := range entries {
		selectors := parseScope(e.Scope)
		rule := themeRule{
			selectors: selectors,
			fg:        parseColor(e.Settings.Foreground),
			bg:        parseColor(e.Settings.Background),
			order:     i,
		}
		if e.Settings.FontStyle != nil {
			rule.font = parseFontStyle(*e.Settings.FontStyle)
		}
		// An entry with no scope provides document defaults.
		if len(selectors) == 0 {
			if rule.fg != nil {
				t.DefaultFG = rule.fg
			}
			if rule.bg != nil {
				t.DefaultBG = rule.bg
			}
			// Keep it as a rule too (matches everything at lowest priority).
			rule.selectors = [][]string{nil}
		}
		t.rules = append(t.rules, rule)
	}
	return t, nil
}

// ParseFile reads and parses a theme file.
func ParseFile(path string) (*Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("theme: read %s: %w", path, err)
	}
	return Parse(data)
}

func parseScope(raw json.RawMessage) [][]string {
	if len(raw) == 0 {
		return nil
	}
	var selectors []string
	// scope may be a single string or an array of strings.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		selectors = strings.Split(s, ",")
	} else {
		var arr []string
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, item := range arr {
				selectors = append(selectors, strings.Split(item, ",")...)
			}
		}
	}
	var out [][]string
	for _, sel := range selectors {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		segs := strings.Fields(sel)
		// Ignore selector operators we do not support (e.g. ">"); keep scope
		// terms only, which degrades gracefully.
		clean := segs[:0]
		for _, seg := range segs {
			if seg == ">" || seg == "-" || strings.HasPrefix(seg, "-") {
				continue
			}
			clean = append(clean, seg)
		}
		if len(clean) > 0 {
			out = append(out, clean)
		}
	}
	return out
}

func parseFontStyle(s string) *fontStyle {
	fs := &fontStyle{}
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		switch tok {
		case "bold":
			fs.bold = true
		case "italic":
			fs.italic = true
		case "underline":
			fs.underline = true
		}
	}
	return fs
}

func parseColor(s string) *RGB {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '#' {
		return nil
	}
	hex := s[1:]
	switch len(hex) {
	case 3, 4: // #RGB or #RGBA
		r := hexVal(hex[0]) * 17
		g := hexVal(hex[1]) * 17
		b := hexVal(hex[2]) * 17
		if r < 0 || g < 0 || b < 0 {
			return nil
		}
		return &RGB{uint8(r), uint8(g), uint8(b)}
	case 6, 8: // #RRGGBB or #RRGGBBAA
		r := hexVal(hex[0])*16 + hexVal(hex[1])
		g := hexVal(hex[2])*16 + hexVal(hex[3])
		b := hexVal(hex[4])*16 + hexVal(hex[5])
		if r < 0 || g < 0 || b < 0 {
			return nil
		}
		return &RGB{uint8(r), uint8(g), uint8(b)}
	default:
		return nil
	}
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

// ---------------------------------------------------------------------------
// Matching
// ---------------------------------------------------------------------------

// Match resolves the style for a token whose scope stack is given outermost
// first. Each visual property (foreground, background, font style) is resolved
// independently to the most specific matching rule that defines it, falling
// back to the theme defaults.
func (t *Theme) Match(scopes []string) Style {
	var style Style
	style.Foreground = t.DefaultFG
	style.Background = t.DefaultBG

	const noMatch = -1
	bestFG, bestBG, bestFont := noMatch, noMatch, noMatch

	for ri := range t.rules {
		r := &t.rules[ri]
		score, ok := ruleScore(r, scopes)
		if !ok {
			continue
		}
		// Iterating rules in declaration order with ">=" means that, among
		// equally specific rules, the later definition wins (TextMate override
		// semantics), while a strictly more specific rule always wins.
		if r.fg != nil && score >= bestFG {
			style.Foreground = r.fg
			bestFG = score
		}
		if r.bg != nil && score >= bestBG {
			style.Background = r.bg
			bestBG = score
		}
		if r.font != nil && score >= bestFont {
			style.Bold = r.font.bold
			style.Italic = r.font.italic
			style.Underline = r.font.underline
			bestFont = score
		}
	}
	return style
}

// ruleScore returns the best selector score for a rule against the scope stack.
func ruleScore(r *themeRule, scopes []string) (int, bool) {
	best := -1
	for _, sel := range r.selectors {
		if len(sel) == 0 {
			// Default rule: matches everything at the lowest score.
			if best < 0 {
				best = 0
			}
			continue
		}
		if s, ok := selectorScore(sel, scopes); ok && s > best {
			best = s
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// selectorScore matches a descendant selector (segments outer..inner) against
// a scope stack (outer..inner). Segments are matched right-to-left, each as a
// scope prefix. The score rewards matching deeper scopes and longer, more
// specific selectors.
func selectorScore(segments, scopes []string) (int, bool) {
	j := len(scopes) - 1
	deepest := -1
	for i := len(segments) - 1; i >= 0; i-- {
		found := -1
		for k := j; k >= 0; k-- {
			if scopePrefix(segments[i], scopes[k]) {
				found = k
				break
			}
		}
		if found == -1 {
			return 0, false
		}
		if i == len(segments)-1 {
			deepest = found
		}
		j = found - 1
	}
	// Score: deepest matched scope depth dominates; selector length and the
	// total dotted specificity refine ties.
	score := (deepest+1)*1000 + len(segments)*50
	for _, seg := range segments {
		score += strings.Count(seg, ".") + 1
	}
	return score, true
}

// scopePrefix reports whether selector segment matches scope, either exactly or
// as a dot-delimited prefix (e.g. "string" matches "string.quoted.double").
func scopePrefix(seg, scope string) bool {
	return scope == seg || strings.HasPrefix(scope, seg+".")
}
