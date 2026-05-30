package grammar

import (
	"encoding/json"
	"fmt"
)

// RawGrammar is the on-disk representation of a TextMate grammar
// (a .tmLanguage.json / .tmLanguage / VSCode grammar file decoded from JSON).
type RawGrammar struct {
	ScopeName  string              `json:"scopeName"`
	Name       string              `json:"name"`
	Patterns   []*RawRule          `json:"patterns"`
	Repository map[string]*RawRule `json:"repository"`
	Injections map[string]*RawRule `json:"injections"`

	// InjectionSelector is set on grammars that are purely an injection.
	InjectionSelector string `json:"injectionSelector"`
}

// RawRule mirrors a single rule object as it appears in a grammar file. A rule
// is interpreted as one of: an include, a single "match", or a "begin"/"end"
// (or "begin"/"while") block. Captures may themselves carry nested patterns.
type RawRule struct {
	// Include references another rule: "#name" (repository), "$self", "$base",
	// or "scope.name" / "scope.name#sub" (another grammar).
	Include string `json:"include"`

	Name        string `json:"name"`
	ContentName string `json:"contentName"`

	Match string `json:"match"`
	Begin string `json:"begin"`
	End   string `json:"end"`
	While string `json:"while"`

	Captures      Captures `json:"captures"`
	BeginCaptures Captures `json:"beginCaptures"`
	EndCaptures   Captures `json:"endCaptures"`
	WhileCaptures Captures `json:"whileCaptures"`

	Patterns []*RawRule `json:"patterns"`

	// ApplyEndPatternLast, when true, tries child patterns before the end
	// pattern at each position rather than the other way around.
	ApplyEndPatternLast intOrBool `json:"applyEndPatternLast"`
}

// Captures maps capture group numbers (as strings, e.g. "0", "1") to the rule
// describing how to scope/sub-tokenize that group.
type Captures map[string]*RawRule

// intOrBool decodes grammar fields that some authors write as 1/0 and others as
// true/false.
type intOrBool bool

func (b *intOrBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", "1":
		*b = true
	case "false", "0", "null", `""`:
		*b = false
	default:
		// Fall back to a best-effort numeric/boolean decode.
		var v interface{}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		switch t := v.(type) {
		case bool:
			*b = intOrBool(t)
		case float64:
			*b = intOrBool(t != 0)
		default:
			return fmt.Errorf("grammar: cannot decode applyEndPatternLast from %s", data)
		}
	}
	return nil
}

// ParseGrammar decodes a raw grammar from its JSON bytes.
func ParseGrammar(data []byte) (*RawGrammar, error) {
	var g RawGrammar
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("grammar: parse: %w", err)
	}
	if g.ScopeName == "" {
		return nil, fmt.Errorf("grammar: missing scopeName")
	}
	return &g, nil
}
