// Package oniglib provides a thin, pure-Go regex layer for the TextMate
// interpreter. It wraps github.com/dlclark/regexp2 (no cgo) and emulates the
// parts of Oniguruma's OnigScanner that the tokenizer relies on:
//
//   - earliest-match selection across an ordered list of patterns,
//   - rune-based (codepoint) match/capture offsets,
//   - position-sensitive handling of the \A (document start) and \G
//     (contiguous anchor) assertions,
//   - back-reference substitution for begin/end "end" patterns.
//
// regexp2 natively supports lookbehind, lookahead, back-references, the \G and
// \A anchors and \x{...} codepoint escapes, which RE2 (the standard library
// regexp package) cannot do, so it is a practical match for Oniguruma syntax.
package oniglib

import (
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// matchTimeout optionally bounds a single regex evaluation so that a
// pathological pattern (catastrophic backtracking) cannot hang the host
// application; on timeout the pattern is treated as non-matching for that
// attempt. A value <= 0 disables the timeout (regexp2's default), which avoids
// per-match timeout bookkeeping in the hot path.
var matchTimeout time.Duration = 0

// neverMatch is a zero-width assertion that can never be satisfied (a position
// cannot be both a word boundary and a non-word boundary). It is used to
// neutralise \A or \G when the current scan position forbids them.
const neverMatch = `\b\B`

// compileOptions are applied to every pattern. We deliberately do NOT use
// regexp2.RE2 so that the engine keeps its Oniguruma/PCRE-compatible behaviour
// (back-references, possessive-ish constructs, etc.).
const compileOptions = regexp2.None

// Capture is a single capture group span expressed in rune (codepoint) offsets.
// Start and End are -1 when the group did not participate in the match.
type Capture struct {
	Start int
	End   int
}

// Empty reports whether the capture is zero-width.
func (c Capture) Empty() bool { return c.Start == c.End }

// MatchResult describes a successful match produced by a Scanner.
type MatchResult struct {
	// PatternIndex is the index, within the Scanner's pattern list, of the
	// pattern that produced the winning (earliest) match.
	PatternIndex int
	// Groups holds the capture spans. Index 0 is the whole match; subsequent
	// entries correspond to numbered capture groups.
	Groups []Capture
}

// Regex is a single compiled TextMate pattern. Because \A and \G must behave
// differently depending on the scan position, a Regex lazily compiles up to
// four variants (the cross product of "allow \A" and "allow \G") and caches
// them. Patterns containing neither anchor compile to a single shared variant.
type Regex struct {
	source string
	hasA   bool
	hasG   bool

	mu       sync.Mutex
	variants map[int]*regexp2.Regexp
	broken   bool // a variant failed to compile; treat as never-matching
}

const (
	optAllowA = 1 << 0
	optAllowG = 1 << 1
)

// NewRegex prepares (but does not yet compile) a pattern. The source is first
// normalized from Oniguruma syntax towards what regexp2 accepts.
func NewRegex(source string) *Regex {
	source = normalizeOniguruma(source)
	return &Regex{
		source:   source,
		hasA:     containsAnchor(source, 'A'),
		hasG:     containsAnchor(source, 'G'),
		variants: make(map[int]*regexp2.Regexp, 1),
	}
}

// normalizeOniguruma rewrites the Oniguruma-specific constructs that regexp2
// rejects into equivalents:
//
//   - possessive quantifiers (a++, a*+, a?+, a{n,m}+) are converted into
//     atomic groups, e.g. a++  ->  (?>a+). This preserves the no-backtracking
//     semantics of possessive matching, which is essential: rewriting them as
//     plain greedy quantifiers can cause catastrophic backtracking on the large
//     declaration/attribute patterns found in real grammars.
//
// Char classes and escaped metacharacters are respected so literal quantifier
// characters are left untouched.
func normalizeOniguruma(source string) string {
	rs := []rune(source)
	out := make([]rune, 0, len(rs)+8)
	inClass := false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\\' && i+1 < len(rs) {
			out = append(out, c, rs[i+1])
			i++
			continue
		}
		if inClass {
			out = append(out, c)
			if c == ']' {
				inClass = false
			}
			continue
		}
		if c == '[' {
			inClass = true
			out = append(out, c)
			continue
		}
		// A '+' immediately following a quantifier marks a possessive
		// quantifier: wrap the already-emitted atom+quantifier atomically.
		if c == '+' && len(out) > 0 {
			switch out[len(out)-1] {
			case '+', '*', '?', '}':
				out = wrapAtomicTail(out)
				continue
			}
		}
		out = append(out, c)
	}
	return string(out)
}

// wrapAtomicTail wraps the trailing "<atom><quantifier>" already present in out
// with an atomic group: ...X<quant>  ->  ...(?>X<quant>). On any ambiguity it
// falls back to leaving out unchanged (which degrades a possessive quantifier
// to greedy rather than corrupting the pattern).
func wrapAtomicTail(out []rune) []rune {
	end := len(out)

	// Locate the start of the quantifier token.
	quantStart := end - 1
	if out[end-1] == '}' {
		quantStart = scanBackTo(out, end-1, '{')
		if quantStart < 0 {
			return out // unmatched: leave as greedy
		}
	}

	// Locate the start of the atom the quantifier applies to.
	atomEnd := quantStart
	if atomEnd <= 0 {
		return out
	}
	prev := out[atomEnd-1]
	var atomStart int
	switch prev {
	case ')':
		atomStart = matchBackward(out, atomEnd-1, '(', ')')
	case ']':
		atomStart = matchBackward(out, atomEnd-1, '[', ']')
	default:
		atomStart = atomEnd - 1
		if atomStart > 0 && isEscaped(out, atomStart) {
			atomStart-- // include the leading backslash of an escaped atom
		}
	}
	if atomStart < 0 {
		return out
	}

	result := make([]rune, 0, len(out)+4)
	result = append(result, out[:atomStart]...)
	result = append(result, '(', '?', '>')
	result = append(result, out[atomStart:end]...)
	result = append(result, ')')
	return result
}

// scanBackTo returns the index of the nearest unescaped open rune at or before
// from, or -1 if none.
func scanBackTo(out []rune, from int, open rune) int {
	for i := from; i >= 0; i-- {
		if out[i] == open && !isEscaped(out, i) {
			return i
		}
	}
	return -1
}

// matchBackward finds the matching open rune for a close rune at closeIdx,
// honouring nesting and escapes, returning the open index or -1.
func matchBackward(out []rune, closeIdx int, open, close rune) int {
	depth := 0
	for i := closeIdx; i >= 0; i-- {
		if isEscaped(out, i) {
			continue
		}
		switch out[i] {
		case close:
			depth++
		case open:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isEscaped reports whether the rune at index i is preceded by an odd number of
// backslashes (and is therefore escaped).
func isEscaped(out []rune, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && out[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// Source returns the original pattern text.
func (r *Regex) Source() string { return r.source }

func (r *Regex) variantKey(allowA, allowG bool) int {
	key := 0
	// Only the anchors actually present affect the compiled form, so patterns
	// without them collapse onto a single cache entry.
	if r.hasA && allowA {
		key |= optAllowA
	}
	if r.hasG && allowG {
		key |= optAllowG
	}
	return key
}

func (r *Regex) compiled(allowA, allowG bool) (*regexp2.Regexp, error) {
	key := r.variantKey(allowA, allowG)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.broken {
		return nil, nil
	}
	if re, ok := r.variants[key]; ok {
		return re, nil
	}

	src := r.source
	if r.hasA && key&optAllowA == 0 {
		src = neutralizeAnchor(src, 'A')
	}
	if r.hasG && key&optAllowG == 0 {
		src = neutralizeAnchor(src, 'G')
	}

	re, err := regexp2.Compile(src, compileOptions)
	if err != nil {
		r.broken = true
		return nil, err
	}
	if matchTimeout > 0 {
		re.MatchTimeout = matchTimeout
	}
	r.variants[key] = re
	return re, nil
}

// Warmup eagerly compiles the pattern's variants so that no regex compilation
// happens later on the matching (per-keystroke) path. Compilation errors are
// swallowed (the pattern is marked broken). It is safe to call concurrently
// across different Regex values.
func (r *Regex) Warmup() {
	// Compile the variants that tokenization actually selects. Patterns without
	// \A/\G collapse to a single cached variant, so this is cheap for them.
	_, _ = r.compiled(false, false)
	if r.hasA || r.hasG {
		_, _ = r.compiled(true, false)
		_, _ = r.compiled(false, true)
		_, _ = r.compiled(true, true)
	}
}

// Broken reports whether the pattern failed to compile (and is therefore
// treated as never-matching).
func (r *Regex) Broken() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.broken
}

// match runs the appropriate variant against text starting at pos and returns
// the capture spans, or nil if there is no match at or after pos. A pattern
// that fails to compile is treated as never-matching (it cannot break
// tokenization of the rest of the line).
func (r *Regex) match(text []rune, pos int, allowA, allowG bool) ([]Capture, error) {
	re, err := r.compiled(allowA, allowG)
	if err != nil || re == nil {
		return nil, nil
	}
	m, err := re.FindRunesMatchStartingAt(text, pos)
	if err != nil || m == nil {
		return nil, nil
	}
	groups := m.Groups()
	caps := make([]Capture, len(groups))
	for i, g := range groups {
		if len(g.Captures) == 0 {
			caps[i] = Capture{Start: -1, End: -1}
			continue
		}
		caps[i] = Capture{Start: g.RuneIndex, End: g.RuneIndex + g.RuneLength}
	}
	return caps, nil
}

// Scanner holds an ordered set of patterns and finds the one whose match begins
// earliest in the text, breaking ties by declaration order (lowest index),
// mirroring Oniguruma's OnigScanner semantics.
type Scanner struct {
	patterns []*Regex
}

// NewScanner compiles (lazily) a scanner from raw pattern sources.
func NewScanner(sources []string) *Scanner {
	pats := make([]*Regex, len(sources))
	for i, s := range sources {
		pats[i] = NewRegex(s)
	}
	return &Scanner{patterns: pats}
}

// NewScannerFromRegexes builds a scanner from already-prepared Regex values.
func NewScannerFromRegexes(regexes []*Regex) *Scanner {
	return &Scanner{patterns: regexes}
}

// Len returns the number of patterns in the scanner.
func (s *Scanner) Len() int { return len(s.patterns) }

// FindNextMatch returns the earliest match at or after pos. allowA indicates
// that \A may match (i.e. we are on the first line of the document) and allowG
// indicates that \G may match (i.e. pos is the current contiguous anchor).
// It returns (nil, nil) when nothing matches.
func (s *Scanner) FindNextMatch(text []rune, pos int, allowA, allowG bool) (*MatchResult, error) {
	best := -1
	var bestCaps []Capture
	for i, p := range s.patterns {
		caps, err := p.match(text, pos, allowA, allowG)
		if err != nil {
			return nil, err
		}
		if caps == nil {
			continue
		}
		start := caps[0].Start
		if best == -1 || start < bestCaps[0].Start {
			best = i
			bestCaps = caps
			// A match exactly at pos is unbeatable; stop early.
			if start == pos {
				break
			}
		}
	}
	if best == -1 {
		return nil, nil
	}
	return &MatchResult{PatternIndex: best, Groups: bestCaps}, nil
}

// SubstituteBackRefs replaces \0..\9 (and \k<n> style is not supported by
// TextMate) in an "end"/"while" pattern source with the regex-escaped text of
// the corresponding capture groups taken from the matching "begin" rule. This
// implements TextMate's dynamic end patterns, e.g. heredocs: begin
// "<<<(\w+)" with end "^\1;?".
func SubstituteBackRefs(source string, captured []string) string {
	var b strings.Builder
	rs := []rune(source)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\\' && i+1 < len(rs) {
			n := rs[i+1]
			if n >= '0' && n <= '9' {
				idx := int(n - '0')
				if idx < len(captured) {
					b.WriteString(escapeRegex(captured[idx]))
				}
				i++
				continue
			}
			// Preserve other escapes verbatim (e.g. \\ , \w).
			b.WriteRune(c)
			b.WriteRune(n)
			i++
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// HasBackRefs reports whether a pattern source references to begin captures.
func HasBackRefs(source string) bool {
	rs := []rune(source)
	for i := 0; i+1 < len(rs); i++ {
		if rs[i] == '\\' {
			n := rs[i+1]
			if n >= '0' && n <= '9' {
				return true
			}
			// Skip the escaped char so "\\1" (literal backslash then 1) is not
			// mistaken for a back-reference.
			i++
		}
	}
	return false
}

// escapeRegex quotes regex metacharacters in s so the captured text is matched
// literally when spliced into a dynamic end pattern.
func escapeRegex(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '#', '-', '&', '~':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// containsAnchor reports whether source contains the \<letter> anchor (\A or
// \G), correctly skipping escaped backslashes so that "\\A" (a literal
// backslash followed by A) is not counted.
func containsAnchor(source string, letter rune) bool {
	rs := []rune(source)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' {
			if i+1 < len(rs) {
				if rs[i+1] == letter {
					return true
				}
				i++ // skip the escaped character
			}
		}
	}
	return false
}

// neutralizeAnchor replaces every \<letter> assertion with a never-matching
// assertion, leaving escaped backslashes untouched.
func neutralizeAnchor(source string, letter rune) string {
	var b strings.Builder
	rs := []rune(source)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			n := rs[i+1]
			if n == letter {
				b.WriteString(neverMatch)
				i++
				continue
			}
			b.WriteRune('\\')
			b.WriteRune(n)
			i++
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}
