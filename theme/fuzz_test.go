package theme

import "testing"

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"name":"t","tokenColors":[]}`))
	f.Add([]byte(`{"colors":{"editor.foreground":"#aabbcc"},"tokenColors":[{"scope":"comment","settings":{"foreground":"#112233"}}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 {
			data = data[:64*1024]
		}
		th, err := Parse(data)
		if err != nil {
			return
		}
		_ = th.Match([]string{"source", "comment.line"})
	})
}
