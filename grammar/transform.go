package grammar

import (
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2/v2"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// transformOrder is the fixed order in which TextMate applies capture-format
// transforms, regardless of the order they are written in the template. This
// mirrors the sequence of bit-flag checks in TextMate's format_string.cc.
var transformOrder = []string{
	"upcase",
	"downcase",
	"capitalize",
	"asciify",
	"urlencode",
	"shellescape",
	"relative",
	"number",
	"duration",
	"dirname",
	"basename",
}

// nowFunc is the clock used by the "relative" transform. It is a variable so
// tests can install a deterministic time.
var nowFunc = time.Now

// applyTransforms applies the requested transforms to s in TextMate's fixed
// order. Unknown transform names are ignored. "titlecase" is an alias for
// "capitalize" (both map to TextMate's kCapitalize).
func applyTransforms(s string, transforms []string) string {
	if len(transforms) == 0 {
		return s
	}
	requested := make(map[string]bool, len(transforms))
	for _, t := range transforms {
		t = strings.TrimSpace(t)
		if t == "titlecase" {
			t = "capitalize"
		}
		requested[t] = true
	}
	for _, name := range transformOrder {
		if !requested[name] {
			continue
		}
		s = applyTransform(name, s)
	}
	return s
}

func applyTransform(name, s string) string {
	switch name {
	case "upcase":
		return strings.ToUpper(s)
	case "downcase":
		return strings.ToLower(s)
	case "capitalize":
		return capitalize(s)
	case "asciify":
		return asciify(s)
	case "urlencode":
		return urlEncode(s)
	case "shellescape":
		return shellEscape(s)
	case "relative":
		return relativeTime(s)
	case "number":
		return formatNumber(s)
	case "duration":
		return formatDuration(s)
	case "dirname":
		return path.Dir(s)
	case "basename":
		return path.Base(s)
	default:
		return s
	}
}

// capitalize reproduces TextMate's English title-casing (kCapitalize). It first
// lowercases all-uppercase words, then uppercases the first letter of each
// significant word (skipping a small set of stop words and very short words
// unless they are at the start or end of the string).
var (
	capWordsRe  = regexp2.MustCompile(`\A\P{Ll}+\z|\b\p{Lu}\P{Lu}+?\b`, regexp2.None)
	capUpcaseRe = regexp2.MustCompile(`^([\W\d]*)(\w[-\w]*)|\b((?!(?:else|from|over|then|when)\b)\w[-\w]{3,}|\w[-\w]*[\W\d]*$)`, regexp2.None)
)

func capitalize(s string) string {
	if s == "" {
		return s
	}
	lowered, err := capWordsRe.ReplaceFunc(s, func(m regexp2.Match) string {
		return strings.ToLower(m.String())
	}, -1, -1)
	if err != nil {
		return s
	}
	out, err := capUpcaseRe.ReplaceFunc(lowered, func(m regexp2.Match) string {
		if g := m.GroupByNumber(1); g != nil && len(g.Captures) > 0 {
			return g.String() + upcaseFirst(m.GroupByNumber(2).String())
		}
		return upcaseFirst(m.String())
	}, -1, -1)
	if err != nil {
		return lowered
	}
	return out
}

// upcaseFirst uppercases only the first rune of s (TextMate's \u escape).
func upcaseFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// asciify strips diacritics and combining marks, approximating TextMate's
// asciify (which additionally uses ICU's //TRANSLIT, unavailable without cgo).
func asciify(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

// urlEncode percent-encodes everything outside the RFC 3986 unreserved set.
func urlEncode(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.' || c == '~':
		return true
	}
	return false
}

// shellEscape ports TextMate's shell_escape: split the value on single quotes,
// single-quote any word containing a shell-special character, and rejoin the
// pieces with an escaped single quote.
func shellEscape(s string) string {
	const special = "|&;<>()$`\\\" \t\n*?[#~=%"
	var b strings.Builder
	parts := strings.Split(s, "'")
	for i, word := range parts {
		if i > 0 {
			b.WriteString(`\'`)
		}
		if strings.ContainsAny(word, special) {
			b.WriteByte('\'')
			b.WriteString(word)
			b.WriteByte('\'')
		} else {
			b.WriteString(word)
		}
	}
	return b.String()
}

// relativeTime parses src as a timestamp and renders a human "... ago" string,
// porting the duration buckets from TextMate's relative_time.
func relativeTime(src string) string {
	now := nowFunc()
	layouts := []string{
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, src)
		if err != nil {
			continue
		}
		d := math.Round(now.Sub(t).Seconds())
		switch {
		case d < 0:
			return "in the future"
		case d < 2:
			return "just now"
		case d < 60:
			return fmt.Sprintf("%.0f seconds ago", d)
		case d < 90:
			return "a minute ago"
		case d < 3570:
			return fmt.Sprintf("%.0f minutes ago", d/60)
		case d < 5400:
			return "an hour ago"
		case d < 84600:
			return fmt.Sprintf("%.0f hours ago", d/(60*60))
		case d < 129600:
			return "a day ago"
		case d < 561600:
			return fmt.Sprintf("%.0f days ago", d/(24*60*60))
		case d < 1036800:
			return "a week ago"
		case d < 2419200:
			return fmt.Sprintf("%.0f weeks ago", d/(7*24*60*60))
		case d < 3952800:
			return "a month ago"
		case d < 30304800:
			return fmt.Sprintf("%.0f months ago", d/(30.5*24*60*60))
		case d < 47304000:
			return "a year ago"
		default:
			return fmt.Sprintf("%.0f years ago", d/(365*24*60*60))
		}
	}
	return src
}

// formatNumber inserts thousands separators into the integer part of each
// number in src, matching TextMate's format_number.
var numberRe = regexp2.MustCompile(`(\d+)(\.\d+)?`, regexp2.None)

func formatNumber(src string) string {
	out, err := numberRe.ReplaceFunc(src, func(m regexp2.Match) string {
		intPart := m.GroupByNumber(1).String()
		frac := m.GroupByNumber(2).String()
		return groupThousands(intPart) + frac
	}, -1, -1)
	if err != nil {
		return src
	}
	return out
}

func groupThousands(digits string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	var b strings.Builder
	first := n % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(digits[:first])
	for i := first; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// formatDuration renders a number of seconds as "d days, h hours, m minutes
// [, s seconds]", matching TextMate's format_duration (seconds are included
// only for durations under ten minutes).
func formatDuration(src string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(src), 64)
	if err != nil {
		return src
	}
	seconds := int64(math.Round(f))
	units := []struct {
		singular, plural string
		amount           int64
		include          bool
	}{
		{"day", "days", seconds / 60 / 60 / 24, true},
		{"hour", "hours", (seconds / 60 / 60) % 24, true},
		{"minute", "minutes", (seconds / 60) % 60, true},
		{"second", "seconds", seconds % 60, seconds < 10*60},
	}
	var parts []string
	for _, u := range units {
		if u.amount != 0 && u.include {
			name := u.plural
			if u.amount == 1 {
				name = u.singular
			}
			parts = append(parts, fmt.Sprintf("%d %s", u.amount, name))
		}
	}
	return strings.Join(parts, ", ")
}
