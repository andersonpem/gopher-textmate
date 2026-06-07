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

// neverMatch is a zero-width assertion that can never be satisfied (an empty
// negative look-ahead fails at every position). It is used to neutralise \A or
// \G when the current scan position forbids them.
const neverMatch = `(?!)`

// neverMatchInClass neutralises \A or \G when they appear inside a character
// class, where an assertion like (?!) or \b\B is not valid. U+FFFF is a
// non-character that effectively never occurs in real text, mirroring
// vscode-textmate's use of \uFFFF as a never-matching sentinel.
const neverMatchInClass = `\x{FFFF}`

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

// normalizeOniguruma rewrites the Oniguruma-specific quantifier constructs that
// regexp2 (.NET semantics) rejects or interprets differently into equivalents.
// It works as a single forward pass that parses each quantifiable atom (escape,
// character class, group/comment, or single character) followed by its optional
// quantifier, so nested classes, (?#...) comments and interval bodies never
// confuse the rewriter. The transformations applied (matching the default
// ONIG_SYNTAX_ONIGURUMA syntax) are:
//
//   - possessive single-char quantifiers a?+, a*+, a++  ->  atomic groups
//     (?>a?), (?>a*), (?>a+). This preserves the no-backtracking semantics,
//     which is essential to avoid catastrophic backtracking on real grammars.
//   - reversed interval a{n,m} with n>m, which Oniguruma defines as the
//     possessive form of {m,n}  ->  (?>a{m,n}).
//   - interval followed by '+' (a{n}+, a{n,m}+, a{n,}+) which is NOT possessive
//     in the default syntax  ->  (?:a{n})+, so regexp2 accepts it.
//   - the {,n} form (== {0,n}) which .NET does not accept  ->  {0,n}.
//
// Invalid braces such as a{abc}+ are left untouched: {abc} is a literal, so the
// trailing '+' is an ordinary quantifier applied to the literal '}'.
func normalizeOniguruma(source string) string {
	rs := []rune(source)
	out := make([]rune, 0, len(rs)+8)
	i := 0
	for i < len(rs) {
		atomStart := len(out)
		ae := scanAtom(rs, i)
		out = append(out, rs[i:ae]...)
		i = ae
		if i >= len(rs) {
			continue
		}

		switch rs[i] {
		case '?', '*', '+':
			out = append(out, rs[i])
			i++
			if i < len(rs) && rs[i] == '+' {
				// Possessive (a?+, a*+, a++): wrap atom+quantifier atomically.
				out = wrapGroup(out, atomStart, "(?>")
				i++ // consume the possessive '+'
			} else if i < len(rs) && rs[i] == '?' {
				// Reluctant (a*?, a+?, a??): regexp2 supports these natively.
				out = append(out, rs[i])
				i++
			}
		case '{':
			iv, ok := parseInterval(rs, i)
			if !ok {
				// Not a valid interval; '{' is a literal character.
				out = append(out, rs[i])
				i++
				continue
			}
			switch {
			case iv.reversed():
				// Oniguruma: {n,m} with n>m is possessive of {m,n}.
				out = append(out, []rune("{"+iv.hi+","+iv.lo+"}")...)
				out = wrapGroup(out, atomStart, "(?>")
				i = iv.end
			case iv.end < len(rs) && rs[iv.end] == '+':
				// {n}+, {n,m}+, {n,}+ are NOT possessive in the default syntax:
				// the '+' is an ordinary quantifier applied to the interval.
				out = append(out, []rune(iv.text())...)
				out = wrapGroup(out, atomStart, "(?:")
				out = append(out, '+')
				i = iv.end + 1
			default:
				out = append(out, []rune(iv.text())...)
				i = iv.end
			}
		}
	}
	return string(out)
}

// wrapGroup rewrites out so that the run from atomStart to the end is enclosed
// in a group introduced by open (e.g. "(?>" or "(?:") and a closing ')'.
func wrapGroup(out []rune, atomStart int, open string) []rune {
	res := make([]rune, 0, len(out)+len(open)+1)
	res = append(res, out[:atomStart]...)
	res = append(res, []rune(open)...)
	res = append(res, out[atomStart:]...)
	res = append(res, ')')
	return res
}

// scanAtom returns the index just past the quantifiable atom that begins at i.
// An atom is an escape (with any \x{...}/\p{...}/\o{...} brace body), a
// character class, a group (or (?#...) comment), or a single character.
func scanAtom(rs []rune, i int) int {
	switch rs[i] {
	case '\\':
		if i+1 >= len(rs) {
			return i + 1
		}
		end := i + 2
		switch rs[i+1] {
		case 'x', 'p', 'P', 'o':
			if end < len(rs) && rs[end] == '{' {
				for end < len(rs) && rs[end] != '}' {
					end++
				}
				if end < len(rs) {
					end++ // include the closing '}'
				}
			}
		}
		return end
	case '[':
		return scanClass(rs, i)
	case '(':
		return scanGroup(rs, i)
	default:
		return i + 1
	}
}

// scanClass returns the index just past the character class beginning at i
// (rs[i] == '['). A ']' immediately after '[' or '[^' is a literal member, not
// the terminator, mirroring Oniguruma ([]] == [\]]). Class scanning follows
// .NET semantics: '[' inside a class is literal and the first subsequent
// unescaped ']' closes it.
func scanClass(rs []rune, i int) int {
	j := i + 1
	if j < len(rs) && rs[j] == '^' {
		j++
	}
	if j < len(rs) && rs[j] == ']' {
		j++ // leading ']' is a literal member
	}
	for j < len(rs) {
		switch rs[j] {
		case '\\':
			j += 2
			continue
		case ']':
			return j + 1
		}
		j++
	}
	return j // unterminated; caller emits the remainder verbatim
}

// scanGroup returns the index just past the group beginning at i (rs[i] ==
// '('). A (?#...) comment ends at the first unescaped ')'. Nested groups and
// character classes are skipped so their parentheses do not unbalance the scan.
func scanGroup(rs []rune, i int) int {
	if i+2 < len(rs) && rs[i+1] == '?' && rs[i+2] == '#' {
		j := i + 3
		for j < len(rs) {
			if rs[j] == '\\' {
				j += 2
				continue
			}
			if rs[j] == ')' {
				return j + 1
			}
			j++
		}
		return j
	}
	j := i + 1
	for j < len(rs) {
		switch rs[j] {
		case '\\':
			j += 2
			continue
		case '[':
			j = scanClass(rs, j)
			continue
		case '(':
			j = scanGroup(rs, j)
			continue
		case ')':
			return j + 1
		}
		j++
	}
	return j // unterminated
}

// interval describes a parsed {..} quantifier body.
type interval struct {
	lo, hi   string // raw digit runs ("" when omitted)
	hasComma bool
	end      int // index just past the closing '}'
}

// reversed reports whether the interval is a reversed range {n,m} with n>m,
// which Oniguruma treats as the possessive form of {m,n}.
func (iv interval) reversed() bool {
	if !iv.hasComma || iv.lo == "" || iv.hi == "" {
		return false
	}
	lo, hi := atoiClamp(iv.lo), atoiClamp(iv.hi)
	return lo > hi
}

// text returns the .NET-acceptable rendering of the interval. The {,n} form is
// rewritten to {0,n} because .NET does not accept an omitted minimum.
func (iv interval) text() string {
	switch {
	case !iv.hasComma:
		return "{" + iv.lo + "}"
	case iv.hi == "":
		return "{" + iv.lo + ",}"
	case iv.lo == "":
		return "{0," + iv.hi + "}"
	default:
		return "{" + iv.lo + "," + iv.hi + "}"
	}
}

// parseInterval parses a quantifier interval beginning at rs[i] == '{'. It
// returns ok=false when the braces do not form a valid Oniguruma interval (so
// the '{' must be treated as a literal). Valid forms: {n}, {n,}, {,m}, {n,m}.
func parseInterval(rs []rune, i int) (interval, bool) {
	j := i + 1
	var iv interval
	for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
		iv.lo += string(rs[j])
		j++
	}
	if j < len(rs) && rs[j] == ',' {
		iv.hasComma = true
		j++
		for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
			iv.hi += string(rs[j])
			j++
		}
	}
	if j >= len(rs) || rs[j] != '}' {
		return interval{}, false
	}
	// Reject empty bodies: {} and {,} are not quantifiers.
	if iv.hasComma {
		if iv.lo == "" && iv.hi == "" {
			return interval{}, false
		}
	} else if iv.lo == "" {
		return interval{}, false
	}
	iv.end = j + 1
	return iv, true
}

// atoiClamp parses a (possibly long) digit run, clamping overflow to a large
// value so that only the lo>hi ordering decision is affected.
func atoiClamp(s string) int {
	const cap = 1 << 30
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
		if n >= cap {
			return cap
		}
	}
	return n
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
				// Consume the whole digit run: \12 is group 12 and \00001 is
				// group 1 (leading zeros are allowed). \0 is the whole match.
				j := i + 1
				for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
					j++
				}
				idx := atoiClamp(string(rs[i+1 : j]))
				if idx < len(captured) {
					b.WriteString(escapeRegex(captured[idx]))
				}
				i = j - 1
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
// construct, leaving escaped backslashes untouched. Inside a character class a
// zero-width assertion is not valid, so a never-occurring codepoint is used
// instead (see neverMatchInClass).
func neutralizeAnchor(source string, letter rune) string {
	var b strings.Builder
	rs := []rune(source)
	inClass := false
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			n := rs[i+1]
			if n == letter {
				if inClass {
					b.WriteString(neverMatchInClass)
				} else {
					b.WriteString(neverMatch)
				}
				i++
				continue
			}
			b.WriteRune('\\')
			b.WriteRune(n)
			i++
			continue
		}
		switch rs[i] {
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			inClass = false
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}
