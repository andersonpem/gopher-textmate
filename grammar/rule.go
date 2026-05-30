package grammar

import "github.com/andersonpem/gopher-textmate/oniglib"

// Rule is a compiled grammar rule. Concrete implementations are MatchRule,
// BeginEndRule, BeginWhileRule and IncludeOnlyRule.
type Rule interface {
	ruleID() int
	name() string
}

type baseRule struct {
	id       int
	ruleName string
}

func (b *baseRule) ruleID() int  { return b.id }
func (b *baseRule) name() string { return b.ruleName }

// MatchRule scopes a single regex match (no nested begin/end region).
type MatchRule struct {
	baseRule
	match    *oniglib.Regex
	captures []*captureRule
}

// BeginEndRule scopes a region delimited by a "begin" and an "end" pattern.
type BeginEndRule struct {
	baseRule
	contentName    string
	begin          *oniglib.Regex
	beginCaptures  []*captureRule
	end            string // raw source; may contain \1..\9 back-references
	endHasBackRefs bool
	endRegex       *oniglib.Regex // precompiled when end has no back-references
	endCaptures    []*captureRule
	applyEndLast   bool
	patterns       []int
}

// BeginWhileRule scopes a region that continues for as long as a "while"
// pattern matches at the start of each subsequent line.
type BeginWhileRule struct {
	baseRule
	contentName      string
	begin            *oniglib.Regex
	beginCaptures    []*captureRule
	while            string
	whileHasBackRefs bool
	whileRegex       *oniglib.Regex
	whileCaptures    []*captureRule
	patterns         []int
}

// IncludeOnlyRule is a rule that only groups other patterns (the grammar root,
// repository groups, and resolved $self/$base/#include targets).
type IncludeOnlyRule struct {
	baseRule
	contentName string
	patterns    []int
}

// captureRule describes how to scope (and optionally re-tokenize) a single
// capture group. ruleID is -1 when the capture has no nested patterns.
type captureRule struct {
	scope  string
	ruleID int
}
