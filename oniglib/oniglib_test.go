package oniglib

import "testing"

func TestScannerEarliestMatch(t *testing.T) {
	s := NewScanner([]string{`bar`, `foo`})
	text := []rune("xx foo bar")
	res, err := s.FindNextMatch(text, 0, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected a match")
	}
	// "foo" starts earlier than "bar", so pattern index 1 should win.
	if res.PatternIndex != 1 {
		t.Errorf("expected pattern 1 (foo) to win, got %d", res.PatternIndex)
	}
	if res.Groups[0].Start != 3 {
		t.Errorf("expected match start 3, got %d", res.Groups[0].Start)
	}
}

func TestScannerTieBreaksByOrder(t *testing.T) {
	s := NewScanner([]string{`a`, `a`})
	res, _ := s.FindNextMatch([]rune("a"), 0, true, true)
	if res == nil || res.PatternIndex != 0 {
		t.Errorf("expected lowest pattern index to win on tie, got %+v", res)
	}
}

func TestAnchorAControlledByAllowA(t *testing.T) {
	r := NewRegex(`\Afoo`)
	// allowA true: \A behaves normally and matches at position 0.
	if caps, _ := r.match([]rune("foo"), 0, true, true); caps == nil {
		t.Error("expected \\A to match when allowA=true")
	}
	// allowA false: \A is neutralised and never matches.
	if caps, _ := r.match([]rune("foo"), 0, false, true); caps != nil {
		t.Error("expected \\A to be neutralised when allowA=false")
	}
}

func TestAnchorGControlledByAllowG(t *testing.T) {
	r := NewRegex(`\Gfoo`)
	if caps, _ := r.match([]rune("foo"), 0, true, true); caps == nil {
		t.Error("expected \\G to match when allowG=true")
	}
	if caps, _ := r.match([]rune("foo"), 0, true, false); caps != nil {
		t.Error("expected \\G to be neutralised when allowG=false")
	}
}

func TestPossessiveQuantifierNormalised(t *testing.T) {
	r := NewRegex(`(?:a)++b`)
	r.Warmup()
	caps, _ := r.match([]rune("aaab"), 0, true, true)
	if caps == nil {
		t.Errorf("expected possessive quantifier to be normalised and match; broken=%v", r.Broken())
	}
}

func TestBackRefSubstitution(t *testing.T) {
	got := SubstituteBackRefs(`^\1;`, []string{"FULL", "EOT"})
	if got != `^EOT;` {
		t.Errorf("expected ^EOT;, got %q", got)
	}
	// Metacharacters in the captured text must be escaped.
	got = SubstituteBackRefs(`\1`, []string{"", "a.b"})
	if got != `a\.b` {
		t.Errorf("expected a\\.b, got %q", got)
	}
}

func TestHasBackRefs(t *testing.T) {
	if !HasBackRefs(`^\1`) {
		t.Error("expected back-reference detection")
	}
	if HasBackRefs(`\\1`) {
		t.Error("escaped backslash before digit is not a back-reference")
	}
	if HasBackRefs(`\w+`) {
		t.Error("\\w is not a back-reference")
	}
}

func TestNormalizeOniguruma(t *testing.T) {
	cases := []struct{ in, want string }{
		// Possessive single-char quantifiers become atomic groups.
		{`a++`, `(?>a+)`},
		{`a*+`, `(?>a*)`},
		{`a?+`, `(?>a?)`},
		{`\.++`, `(?>\.+)`},
		{`(?:a)++`, `(?>(?:a)+)`},
		// Reluctant quantifiers are left for regexp2 to handle natively.
		{`a*?`, `a*?`},
		{`a+?b`, `a+?b`},
		// Possessive wrapping must respect nested classes and comments.
		{`([)])++`, `(?>([)])+)`},
		{`(a(?#comment [ comment))++`, `(?>(a(?#comment [ comment))+)`},
		// Interval validation.
		{`a{abc}+`, `a{abc}+`},     // not a valid interval: left untouched
		{`a{3,2}`, `(?>a{2,3})`},   // reversed range is possessive of {2,3}
		{`a{3}+`, `(?:a{3})+`},     // {n}+ is not possessive in default syntax
		{`a{2,3}+`, `(?:a{2,3})+`}, // {n,m}+ is not possessive either
		{`a{2,}+`, `(?:a{2,})+`},   // {n,}+ is not possessive either
		{`a{,3}`, `a{0,3}`},        // {,n} rewritten to {0,n} for regexp2
		{`a{,3}+`, `(?:a{0,3})+`},  // combined with the non-possessive '+'
		{`a{2,3}`, `a{2,3}`},       // normal interval untouched
		{`a{3}`, `a{3}`},           // fixed interval untouched
		// Quantifier characters inside a class are literal, not quantifiers.
		{`[a+]+`, `[a+]+`},
		{`[a*]++`, `(?>[a*]+)`},
	}
	for _, c := range cases {
		if got := normalizeOniguruma(c.in); got != c.want {
			t.Errorf("normalizeOniguruma(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLeadingBracketIsLiteral(t *testing.T) {
	cases := []struct {
		pattern, text string
		wantMatch     bool
	}{
		{`[]]`, `]`, true}, // []] == [\]]
		{`[]]`, `a`, false},
		{`[^]]`, `a`, true}, // [^]] negates a class containing ']'
		{`[^]]`, `]`, false},
		{`[]abc]`, `b`, true}, // leading ']' plus a,b,c
		{`[]abc]`, `x`, false},
	}
	for _, c := range cases {
		r := NewRegex(c.pattern)
		r.Warmup()
		if r.Broken() {
			t.Errorf("pattern %q failed to compile", c.pattern)
			continue
		}
		caps, _ := r.match([]rune(c.text), 0, true, true)
		if (caps != nil) != c.wantMatch {
			t.Errorf("%q against %q: match=%v, want %v", c.pattern, c.text, caps != nil, c.wantMatch)
		}
	}
}

func TestPossessiveWithNestedConstructsCompiles(t *testing.T) {
	for _, p := range []string{`([)])++`, `(a(?#comment [ comment))++`} {
		r := NewRegex(p)
		r.Warmup()
		if r.Broken() {
			t.Errorf("pattern %q should compile after normalization", p)
		}
	}
}

func TestReversedIntervalMatches(t *testing.T) {
	// a{3,2} is the possessive form of a{2,3}: it matches 2 or 3 a's.
	r := NewRegex(`a{3,2}`)
	r.Warmup()
	if r.Broken() {
		t.Fatalf("a{3,2} should compile, got broken")
	}
	caps, _ := r.match([]rune("aaaa"), 0, true, true)
	if caps == nil {
		t.Fatal("expected a match")
	}
	if got := caps[0].End - caps[0].Start; got != 3 {
		t.Errorf("expected to match 3 a's (max of range), matched %d", got)
	}
}

func TestNeutralizeAnchorClassAware(t *testing.T) {
	if got := neutralizeAnchor(`\Afoo`, 'A'); got != `(?!)foo` {
		t.Errorf("outside class: got %q, want %q", got, `(?!)foo`)
	}
	if got := neutralizeAnchor(`[\A]`, 'A'); got != `[\x{FFFF}]` {
		t.Errorf("inside class: got %q, want %q", got, `[\x{FFFF}]`)
	}
	if got := neutralizeAnchor(`\G[\G]`, 'G'); got != `(?!)[\x{FFFF}]` {
		t.Errorf("mixed: got %q, want %q", got, `(?!)[\x{FFFF}]`)
	}
}

func TestMultiDigitBackRefs(t *testing.T) {
	captured := make([]string, 13)
	captured[0] = "WHOLE"
	captured[1] = "one"
	captured[12] = "twelve"
	cases := []struct{ in, want string }{
		{`\0`, `WHOLE`},
		{`\1`, `one`},
		{`\12`, `twelve`}, // two-digit group
		{`\00001`, `one`}, // leading zeros
		{`\999`, ``},      // out of range: dropped
		{`\\1`, `\\1`},    // literal backslash then 1: not a back-reference
		{`\\\1`, `\\one`}, // literal backslash then back-reference
	}
	for _, c := range cases {
		if got := SubstituteBackRefs(c.in, captured); got != c.want {
			t.Errorf("SubstituteBackRefs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnicodeCodepointEscapes(t *testing.T) {
	// \x{...} with codepoints beyond U+FFFF must compile and match.
	r := NewRegex(`[\x{7f}-\x{10ffff}]+`)
	caps, _ := r.match([]rune("héllo☃"), 0, true, true)
	if caps == nil {
		t.Errorf("expected unicode class to match; broken=%v", r.Broken())
	}
}
