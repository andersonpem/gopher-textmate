// Package render turns scoped tokens plus a theme into ANSI escape sequences
// suitable for a colour terminal. It supports 24-bit truecolor and the xterm
// 256-colour palette, with a no-colour mode for plain output.
package render

import (
	"os"
	"strconv"
	"strings"

	"github.com/andersonpem/gopher-textmate/grammar"
	"github.com/andersonpem/gopher-textmate/theme"
)

// ColorMode selects how colours are encoded.
type ColorMode int

const (
	// None disables colour output (plain text).
	None ColorMode = iota
	// Color256 uses the xterm 256-colour palette.
	Color256
	// TrueColor uses 24-bit RGB escape sequences.
	TrueColor
)

const (
	reset = "\x1b[0m"
)

// Options configures rendering.
type Options struct {
	Mode ColorMode
	// Background, when true, also emits each token's background colour.
	Background bool
}

// DetectColorMode inspects the environment (NO_COLOR, COLORTERM, TERM) to pick
// a sensible default colour mode.
func DetectColorMode() ColorMode {
	if os.Getenv("NO_COLOR") != "" {
		return None
	}
	if ct := strings.ToLower(os.Getenv("COLORTERM")); strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return TrueColor
	}
	term := os.Getenv("TERM")
	switch {
	case term == "" || term == "dumb":
		return None
	case strings.Contains(term, "256color"):
		return Color256
	default:
		return Color256
	}
}

// RenderLine renders a single tokenized line to a string containing ANSI escape
// sequences. line is the original text; tokens carry rune offsets into it.
func RenderLine(line string, tokens []grammar.Token, th *theme.Theme, opts Options) string {
	runes := []rune(line)
	var b strings.Builder
	pos := 0
	for _, tok := range tokens {
		if tok.Start > pos {
			b.WriteString(string(runes[pos:clamp(tok.Start, len(runes))]))
		}
		start := clamp(tok.Start, len(runes))
		end := clamp(tok.End, len(runes))
		if end <= start {
			continue
		}
		text := string(runes[start:end])
		if opts.Mode == None {
			b.WriteString(text)
		} else {
			style := th.Match(tok.Scopes)
			seq := sgr(style, opts)
			if seq == "" {
				b.WriteString(text)
			} else {
				b.WriteString(seq)
				b.WriteString(text)
				b.WriteString(reset)
			}
		}
		pos = end
	}
	if pos < len(runes) {
		b.WriteString(string(runes[pos:]))
	}
	return b.String()
}

func clamp(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

func sgr(style theme.Style, opts Options) string {
	var codes []string
	if style.Bold {
		codes = append(codes, "1")
	}
	if style.Italic {
		codes = append(codes, "3")
	}
	if style.Underline {
		codes = append(codes, "4")
	}
	if style.Foreground != nil {
		codes = append(codes, colorCodes(*style.Foreground, opts.Mode, false)...)
	}
	if opts.Background && style.Background != nil {
		codes = append(codes, colorCodes(*style.Background, opts.Mode, true)...)
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func colorCodes(c theme.RGB, mode ColorMode, background bool) []string {
	lead := "38"
	if background {
		lead = "48"
	}
	switch mode {
	case TrueColor:
		return []string{lead, "2", itoa(int(c.R)), itoa(int(c.G)), itoa(int(c.B))}
	case Color256:
		return []string{lead, "5", itoa(to256(c))}
	default:
		return nil
	}
}

func itoa(v int) string { return strconv.Itoa(v) }

// to256 maps a 24-bit colour to the nearest xterm-256 palette index, choosing
// between the 6x6x6 colour cube and the 24-step grayscale ramp.
func to256(c theme.RGB) int {
	r, g, b := int(c.R), int(c.G), int(c.B)

	// Candidate from the 6x6x6 colour cube.
	ci := 16 + 36*cubeIndex(r) + 6*cubeIndex(g) + cubeIndex(b)
	cr, cg, cb := cubeLevels[cubeIndex(r)], cubeLevels[cubeIndex(g)], cubeLevels[cubeIndex(b)]

	// Candidate from the grayscale ramp (232..255).
	gray := (r + g + b) / 3
	gi := grayIndex(gray)
	gv := 8 + gi*10
	gIdx := 232 + gi

	if dist(r, g, b, cr, cg, cb) <= dist(r, g, b, gv, gv, gv) {
		return ci
	}
	return gIdx
}

var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

func cubeIndex(v int) int {
	best, bestD := 0, 1<<30
	for i, level := range cubeLevels {
		d := abs(v - level)
		if d < bestD {
			bestD, best = d, i
		}
	}
	return best
}

func grayIndex(v int) int {
	idx := (v - 8 + 5) / 10
	if idx < 0 {
		idx = 0
	}
	if idx > 23 {
		idx = 23
	}
	return idx
}

func dist(r, g, b, r2, g2, b2 int) int {
	dr, dg, db := r-r2, g-g2, b-b2
	return dr*dr + dg*dg + db*db
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
