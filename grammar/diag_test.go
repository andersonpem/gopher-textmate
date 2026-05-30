package grammar

import (
	"testing"

	"github.com/dlclark/regexp2/v2"
)

func TestDiagBrokenPatterns(t *testing.T) {
	g := loadPHP(t)
	// Force compilation of every rule's regex by probing it.
	probe := []rune("x")
	broken := 0
	total := 0
	var samples []string
	for _, r := range g.registry.rules {
		var srcs []string
		switch rt := r.(type) {
		case *MatchRule:
			srcs = append(srcs, rt.match.Source())
			_, _ = matchRegex(rt.match, probe, 0, true, true)
			if rt.match.Broken() {
				broken++
				if len(samples) < 20 {
					samples = append(samples, rt.match.Source())
				}
			}
			total++
		case *BeginEndRule:
			_, _ = matchRegex(rt.begin, probe, 0, true, true)
			if rt.begin.Broken() {
				broken++
				if len(samples) < 20 {
					samples = append(samples, rt.begin.Source())
				}
			}
			total++
			if rt.endRegex != nil {
				_, _ = matchRegex(rt.endRegex, probe, 0, true, true)
				if rt.endRegex.Broken() {
					broken++
					if len(samples) < 20 {
						samples = append(samples, rt.endRegex.Source())
					}
				}
				total++
			}
		}
		_ = srcs
	}
	t.Logf("broken %d / %d patterns", broken, total)
	for _, s := range samples {
		_, err := regexp2.Compile(s, regexp2.None)
		t.Logf("BROKEN: %q -> %v", s, err)
	}
}
