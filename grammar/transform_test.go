package grammar

import (
	"testing"
	"time"
)

func TestApplyTransformsSingle(t *testing.T) {
	cases := []struct {
		name      string
		transform string
		in, want  string
	}{
		{"upcase", "upcase", "Hello World", "HELLO WORLD"},
		{"downcase", "downcase", "Hello World", "hello world"},
		{"capitalize", "capitalize", "hello world", "Hello World"},
		{"titlecase-alias", "titlecase", "hello world", "Hello World"},
		{"capitalize-allcaps", "capitalize", "HELLO", "Hello"},
		{"asciify", "asciify", "café déjà", "cafe deja"},
		{"asciify-plain", "asciify", "hello", "hello"},
		{"urlencode", "urlencode", "a b/c?d", "a%20b%2Fc%3Fd"},
		{"urlencode-unreserved", "urlencode", "A-z_0.9~", "A-z_0.9~"},
		{"shellescape-plain", "shellescape", "hello", "hello"},
		{"shellescape-special", "shellescape", "a b", "'a b'"},
		{"shellescape-quote", "shellescape", "it's", `it\'s`},
		{"number", "number", "1234567", "1,234,567"},
		{"number-decimal", "number", "1234.5678", "1,234.5678"},
		{"number-small", "number", "42", "42"},
		{"duration", "duration", "3661", "1 hour, 1 minute"}, // seconds dropped (>= 10 min)
		{"duration-seconds", "duration", "61", "1 minute, 1 second"},
		{"duration-days", "duration", "90000", "1 day, 1 hour"},
		{"dirname", "dirname", "foo/bar/baz", "foo/bar"},
		{"basename", "basename", "foo/bar/baz", "baz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyTransforms(c.in, []string{c.transform}); got != c.want {
				t.Errorf("applyTransforms(%q, /%s) = %q, want %q", c.in, c.transform, got, c.want)
			}
		})
	}
}

func TestApplyTransformsChainedFixedOrder(t *testing.T) {
	// Transforms apply in TextMate's fixed order regardless of written order:
	// downcase runs before capitalize, so both orderings yield the same result.
	in := "HELLO WORLD"
	want := "Hello World"
	if got := applyTransforms(in, []string{"downcase", "capitalize"}); got != want {
		t.Errorf("downcase/capitalize = %q, want %q", got, want)
	}
	if got := applyTransforms(in, []string{"capitalize", "downcase"}); got != want {
		t.Errorf("capitalize/downcase (written reversed) = %q, want %q", got, want)
	}
}

func TestApplyTransformsUnknownIgnored(t *testing.T) {
	if got := applyTransforms("hello", []string{"bogus"}); got != "hello" {
		t.Errorf("unknown transform should be a no-op, got %q", got)
	}
}

func TestRelativeTimeDeterministic(t *testing.T) {
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	orig := nowFunc
	nowFunc = func() time.Time { return fixed }
	defer func() { nowFunc = orig }()

	cases := []struct{ in, want string }{
		{"2026-06-01 11:59:00", "a minute ago"},
		{"2026-06-01 11:00:00", "an hour ago"},
		{"2026-05-31 12:00:00", "a day ago"},
		{"2026-06-01 13:00:00", "in the future"},
	}
	for _, c := range cases {
		if got := relativeTime(c.in); got != c.want {
			t.Errorf("relativeTime(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseGroupTemplate(t *testing.T) {
	cases := []struct {
		inner          string
		wantNum        int
		wantTransforms []string
	}{
		{"1", 1, nil},
		{"1:/downcase", 1, []string{"downcase"}},
		{"1:/downcase/capitalize", 1, []string{"downcase", "capitalize"}},
		{"12:/upcase", 12, []string{"upcase"}},
		{"bogus", -1, nil},
	}
	for _, c := range cases {
		num, transforms := parseGroupTemplate(c.inner)
		if num != c.wantNum {
			t.Errorf("parseGroupTemplate(%q) num = %d, want %d", c.inner, num, c.wantNum)
		}
		if len(transforms) != len(c.wantTransforms) {
			t.Errorf("parseGroupTemplate(%q) transforms = %v, want %v", c.inner, transforms, c.wantTransforms)
			continue
		}
		for i := range transforms {
			if transforms[i] != c.wantTransforms[i] {
				t.Errorf("parseGroupTemplate(%q) transforms = %v, want %v", c.inner, transforms, c.wantTransforms)
				break
			}
		}
	}
}
