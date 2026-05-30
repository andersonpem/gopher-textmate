package grammar

import "strings"

// injection associates a (compiled) set of patterns with one or more scope
// selectors. When the current scope path matches a selector, the injection's
// patterns participate in tokenization.
//
// Selector support is intentionally basic: a selector matches when every scope
// term it lists is a prefix of some scope currently on the stack. Operators
// other than descendant nesting (e.g. "-" exclusions, "|" alternation beyond
// the top-level comma split, and the "L:"/"R:" prefixes) are parsed leniently
// and otherwise ignored, so advanced selectors degrade gracefully rather than
// breaking tokenization.
type injection struct {
	selectors [][]string // OR of selectors; each selector is an AND of scope terms
	ruleID    int
}

func (g *Grammar) parseInjections() {
	if len(g.raw.Injections) == 0 {
		return
	}
	for key, raw := range g.raw.Injections {
		if raw == nil {
			continue
		}
		sels := parseInjectionSelector(key)
		if len(sels) == 0 {
			continue
		}
		ruleID := g.compileRule(&RawRule{Patterns: raw.Patterns})
		if ruleID == 0 {
			continue
		}
		g.injections = append(g.injections, &injection{selectors: sels, ruleID: ruleID})
	}
}

func parseInjectionSelector(key string) [][]string {
	var out [][]string
	for _, part := range strings.Split(key, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Drop the "L:"/"R:" location prefix.
		if len(part) > 2 && (part[0] == 'L' || part[0] == 'R') && part[1] == ':' {
			part = part[2:]
		}
		// Strip grouping parentheses and treat the remainder as space-separated
		// scope terms, ignoring exclusions and operators.
		part = strings.NewReplacer("(", " ", ")", " ", "|", " ", "&", " ").Replace(part)
		var terms []string
		for _, tok := range strings.Fields(part) {
			if strings.HasPrefix(tok, "-") {
				continue // exclusion: ignored in this basic matcher
			}
			terms = append(terms, tok)
		}
		if len(terms) > 0 {
			out = append(out, terms)
		}
	}
	return out
}

func (inj *injection) matches(scopes []string) bool {
	for _, sel := range inj.selectors {
		if selectorMatches(sel, scopes) {
			return true
		}
	}
	return false
}

func selectorMatches(terms, scopes []string) bool {
	for _, term := range terms {
		if !scopeListHasPrefix(scopes, term) {
			return false
		}
	}
	return true
}

func scopeListHasPrefix(scopes []string, term string) bool {
	for _, s := range scopes {
		if s == term || strings.HasPrefix(s, term+".") {
			return true
		}
	}
	return false
}

// injectionPatterns returns the leaf patterns contributed by injections whose
// selector matches the given scope path.
func (g *Grammar) injectionPatterns(scopes []string) *collectedPatterns {
	if len(g.injections) == 0 {
		return nil
	}
	b := &cpBuilder{}
	for _, inj := range g.injections {
		if !inj.matches(scopes) {
			continue
		}
		cp := g.collectedFor(inj.ruleID)
		b.regexes = append(b.regexes, cp.regexes...)
		b.ruleIDs = append(b.ruleIDs, cp.ruleIDs...)
	}
	if len(b.regexes) == 0 {
		return nil
	}
	return &collectedPatterns{regexes: b.regexes, ruleIDs: b.ruleIDs}
}
