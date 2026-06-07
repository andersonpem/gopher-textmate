package oniglib

import "testing"

func FuzzScannerFindNextMatch(f *testing.F) {
	f.Add(`\w+`, "foo bar")
	f.Add(`(?x)\bfoo\b`, "xx foo bar")
	f.Fuzz(func(t *testing.T, pattern, text string) {
		if len(pattern) > 2048 {
			pattern = pattern[:2048]
		}
		if len(text) > 2048 {
			text = text[:2048]
		}
		sc := NewScanner([]string{pattern})
		_, _ = sc.FindNextMatch([]rune(text), 0, true, true)
	})
}
