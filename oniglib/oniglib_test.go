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

func TestUnicodeCodepointEscapes(t *testing.T) {
	// \x{...} with codepoints beyond U+FFFF must compile and match.
	r := NewRegex(`[\x{7f}-\x{10ffff}]+`)
	caps, _ := r.match([]rune("héllo☃"), 0, true, true)
	if caps == nil {
		t.Errorf("expected unicode class to match; broken=%v", r.Broken())
	}
}
