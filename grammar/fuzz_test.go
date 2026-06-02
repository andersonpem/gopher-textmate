package grammar

import "testing"

func FuzzApplyTransforms(f *testing.F) {
	f.Add("hello world", "capitalize")
	f.Add("1234567", "number")
	f.Add("café déjà", "asciify")
	f.Fuzz(func(t *testing.T, s, transform string) {
		if len(s) > 4096 {
			s = s[:4096]
		}
		_ = applyTransforms(s, []string{transform})
	})
}
