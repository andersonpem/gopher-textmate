package grammar

import (
	"strconv"
	"strings"
	"sync"

	"github.com/andersonpem/gopher-textmate/oniglib"
)

// Token is a contiguous run of characters that share the same scope stack.
// Start and End are rune (codepoint) offsets into the tokenized line, with End
// exclusive. Scopes is ordered from the outermost (grammar) scope to the
// innermost.
//
// Scopes must be treated as immutable: the slice may be shared between many
// tokens for performance. Copy it before mutating.
type Token struct {
	Start  int
	End    int
	Scopes []string
}

// Grammar is a compiled TextMate grammar ready to tokenize text. Obtain one via
// Registry.Grammar. A Grammar is safe for concurrent tokenization once the
// owning Registry has finished loading grammars.
type Grammar struct {
	scopeName  string
	registry   *Registry
	raw        *RawGrammar
	rootRuleID int

	mu         sync.Mutex
	ruleCache  map[*RawRule]int
	repoCache  map[string]int
	childCache map[int]*collectedPatterns
	beCache    map[int]*frameScanner
	injections []*injection
}

// frameScanner is a ready-to-run scanner for an active rule, with the parallel
// rule ids and the index of the "end" pattern (-1 if the frame has no end).
type frameScanner struct {
	scanner  *oniglib.Scanner
	ruleIDs  []int
	endIndex int
}

// ScopeName returns the grammar's base scope (e.g. "source.php").
func (g *Grammar) ScopeName() string { return g.scopeName }

func newGrammar(reg *Registry, raw *RawGrammar) *Grammar {
	return &Grammar{
		scopeName:  raw.ScopeName,
		registry:   reg,
		raw:        raw,
		ruleCache:  make(map[*RawRule]int),
		repoCache:  make(map[string]int),
		childCache: make(map[int]*collectedPatterns),
		beCache:    make(map[int]*frameScanner),
	}
}

// ---------------------------------------------------------------------------
// Compilation
// ---------------------------------------------------------------------------

func (g *Grammar) compileRule(raw *RawRule) int {
	if raw == nil {
		return 0
	}
	if id, ok := g.ruleCache[raw]; ok {
		return id
	}

	switch {
	case raw.Match != "":
		r := &MatchRule{baseRule: baseRule{ruleName: raw.Name}, match: oniglib.NewRegex(raw.Match)}
		id := g.registry.registerRule(r)
		g.ruleCache[raw] = id
		r.captures = g.compileCaptures(captureSet(raw.Captures, nil))
		return id

	case raw.Begin != "" && raw.While != "":
		r := &BeginWhileRule{
			baseRule:    baseRule{ruleName: raw.Name},
			contentName: raw.ContentName,
			begin:       oniglib.NewRegex(raw.Begin),
			while:       raw.While,
		}
		id := g.registry.registerRule(r)
		g.ruleCache[raw] = id
		r.beginCaptures = g.compileCaptures(captureSet(raw.BeginCaptures, raw.Captures))
		r.whileHasBackRefs = oniglib.HasBackRefs(raw.While)
		if !r.whileHasBackRefs {
			r.whileRegex = oniglib.NewRegex(raw.While)
		}
		r.whileCaptures = g.compileCaptures(captureSet(raw.WhileCaptures, raw.Captures))
		r.patterns = g.compilePatterns(raw.Patterns)
		return id

	case raw.Begin != "":
		r := &BeginEndRule{
			baseRule:     baseRule{ruleName: raw.Name},
			contentName:  raw.ContentName,
			begin:        oniglib.NewRegex(raw.Begin),
			end:          raw.End,
			applyEndLast: bool(raw.ApplyEndPatternLast),
		}
		id := g.registry.registerRule(r)
		g.ruleCache[raw] = id
		r.beginCaptures = g.compileCaptures(captureSet(raw.BeginCaptures, raw.Captures))
		r.endHasBackRefs = oniglib.HasBackRefs(raw.End)
		if !r.endHasBackRefs {
			r.endRegex = oniglib.NewRegex(raw.End)
		}
		r.endCaptures = g.compileCaptures(captureSet(raw.EndCaptures, raw.Captures))
		r.patterns = g.compilePatterns(raw.Patterns)
		return id

	default:
		r := &IncludeOnlyRule{baseRule: baseRule{ruleName: raw.Name}, contentName: raw.ContentName}
		id := g.registry.registerRule(r)
		g.ruleCache[raw] = id
		r.patterns = g.compilePatterns(raw.Patterns)
		return id
	}
}

// captureSet selects the effective capture map: a dedicated map if present,
// otherwise the shared "captures" map.
func captureSet(specific, fallback Captures) Captures {
	if specific != nil {
		return specific
	}
	return fallback
}

func (g *Grammar) compilePatterns(raws []*RawRule) []int {
	out := make([]int, 0, len(raws))
	for _, p := range raws {
		if p == nil {
			continue
		}
		if p.Include != "" {
			if id := g.resolveInclude(p.Include); id != 0 {
				out = append(out, id)
			}
			continue
		}
		if id := g.compileRule(p); id != 0 {
			out = append(out, id)
		}
	}
	return out
}

func (g *Grammar) compileCaptures(caps Captures) []*captureRule {
	if len(caps) == 0 {
		return nil
	}
	max := -1
	parsed := make(map[int]*RawRule, len(caps))
	for k, v := range caps {
		idx, err := strconv.Atoi(k)
		if err != nil || idx < 0 {
			continue
		}
		parsed[idx] = v
		if idx > max {
			max = idx
		}
	}
	if max < 0 {
		return nil
	}
	out := make([]*captureRule, max+1)
	for idx, v := range parsed {
		if v == nil {
			continue
		}
		cr := &captureRule{scope: v.Name, ruleID: -1}
		if len(v.Patterns) > 0 {
			cr.ruleID = g.compileRule(&RawRule{Patterns: v.Patterns})
		}
		out[idx] = cr
	}
	return out
}

// resolveInclude maps an include directive to a compiled rule id, or 0 if it
// cannot be resolved (e.g. an unregistered external grammar), in which case the
// include is skipped.
func (g *Grammar) resolveInclude(include string) int {
	switch {
	case include == "":
		return 0
	case include[0] == '#':
		return g.repositoryRule(include[1:])
	case include == "$self" || include == "$base":
		// $base is approximated as $self; for single-grammar tokenization they
		// are identical, and most grammars rely on $self.
		return g.rootRuleID
	default:
		scope, sub := include, ""
		if i := strings.IndexByte(include, '#'); i >= 0 {
			scope, sub = include[:i], include[i+1:]
		}
		ext, err := g.registry.grammarLocked(scope)
		if err != nil {
			return 0 // grammar not registered; skip gracefully
		}
		if sub != "" {
			return ext.repositoryRule(sub)
		}
		return ext.rootRuleID
	}
}

func (g *Grammar) repositoryRule(name string) int {
	if id, ok := g.repoCache[name]; ok {
		return id
	}
	raw, ok := g.raw.Repository[name]
	if !ok || raw == nil {
		return 0
	}
	id := g.compileRule(raw)
	g.repoCache[name] = id
	return id
}

// ---------------------------------------------------------------------------
// Pattern collection (scanner sources)
// ---------------------------------------------------------------------------

type collectedPatterns struct {
	regexes []*oniglib.Regex
	ruleIDs []int
	scanner *oniglib.Scanner
}

type cpBuilder struct {
	regexes []*oniglib.Regex
	ruleIDs []int
}

func (b *cpBuilder) add(re *oniglib.Regex, id int) {
	b.regexes = append(b.regexes, re)
	b.ruleIDs = append(b.ruleIDs, id)
}

func (g *Grammar) patternIDsOf(ruleID int) []int {
	switch r := g.registry.rule(ruleID).(type) {
	case *IncludeOnlyRule:
		return r.patterns
	case *BeginEndRule:
		return r.patterns
	case *BeginWhileRule:
		return r.patterns
	default:
		return nil
	}
}

func (g *Grammar) collect(ruleID int, b *cpBuilder, visited map[int]bool) {
	switch r := g.registry.rule(ruleID).(type) {
	case *MatchRule:
		b.add(r.match, ruleID)
	case *BeginEndRule:
		b.add(r.begin, ruleID)
	case *BeginWhileRule:
		b.add(r.begin, ruleID)
	case *IncludeOnlyRule:
		if visited[ruleID] {
			return
		}
		visited[ruleID] = true
		for _, cid := range r.patterns {
			g.collect(cid, b, visited)
		}
	}
}

func (g *Grammar) collectedFor(ruleID int) *collectedPatterns {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.childCache[ruleID]; ok {
		return c
	}
	b := &cpBuilder{}
	visited := map[int]bool{ruleID: true}
	for _, pid := range g.patternIDsOf(ruleID) {
		g.collect(pid, b, visited)
	}
	c := &collectedPatterns{
		regexes: b.regexes,
		ruleIDs: b.ruleIDs,
		scanner: oniglib.NewScannerFromRegexes(b.regexes),
	}
	g.childCache[ruleID] = c
	return c
}

// ---------------------------------------------------------------------------
// State stack
// ---------------------------------------------------------------------------

// StateStack is the opaque tokenizer state carried from one line to the next.
// A nil StateStack means "start of document".
type StateStack struct {
	parent        *StateStack
	rule          int
	endRegex      *oniglib.Regex
	whileRegex    *oniglib.Regex
	nameScopes    []string
	contentScopes []string
	depth         int
}

// Depth returns how many begin/end (or begin/while) regions are currently open.
func (s *StateStack) Depth() int {
	if s == nil {
		return 0
	}
	return s.depth
}

// Equals reports whether two tokenizer states are equivalent. This is used by
// incremental tokenizers to detect when re-tokenizing an edited region has
// "converged" with the previously cached state, so the remaining lines can be
// reused unchanged. Two states are equal when their rule chains, resolved
// end/while patterns, and accumulated scopes match.
func (s *StateStack) Equals(o *StateStack) bool {
	if s == o {
		return true
	}
	if s == nil || o == nil {
		return false
	}
	// The top frame's contentScopes contains the full ancestor scope path, so
	// comparing it covers scope equality for the whole chain.
	if !equalStrings(s.contentScopes, o.contentScopes) || !equalStrings(s.nameScopes, o.nameScopes) {
		return false
	}
	for a, b := s, o; ; a, b = a.parent, b.parent {
		if a == b {
			return true
		}
		if a == nil || b == nil {
			return false
		}
		if a.rule != b.rule || a.depth != b.depth {
			return false
		}
		if regexSource(a.endRegex) != regexSource(b.endRegex) {
			return false
		}
		if regexSource(a.whileRegex) != regexSource(b.whileRegex) {
			return false
		}
	}
}

func regexSource(r *oniglib.Regex) string {
	if r == nil {
		return ""
	}
	return r.Source()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (g *Grammar) rootStack() *StateStack {
	base := []string{g.scopeName}
	return &StateStack{rule: g.rootRuleID, nameScopes: base, contentScopes: base}
}

// resolveScopeName expands a scope-name template that references match
// captures, e.g. "keyword.control.$1.php" or "entity.name.tag.${1:/downcase}".
// Transforms (see applyTransforms) may be chained, e.g. "${1:/downcase/capitalize}".
// Templates with no "$" are returned unchanged.
func resolveScopeName(tmpl string, line []rune, groups []oniglib.Capture) string {
	if !strings.ContainsRune(tmpl, '$') {
		return tmpl
	}
	rs := []rune(tmpl)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '$' && i+1 < len(rs) {
			if rs[i+1] >= '0' && rs[i+1] <= '9' {
				b.WriteString(captureText(line, groups, int(rs[i+1]-'0'), nil))
				i++
				continue
			}
			if rs[i+1] == '{' {
				j := i + 2
				for j < len(rs) && rs[j] != '}' {
					j++
				}
				if j < len(rs) {
					num, transforms := parseGroupTemplate(string(rs[i+2 : j]))
					b.WriteString(captureText(line, groups, num, transforms))
					i = j
					continue
				}
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

// parseGroupTemplate parses the body of a ${...} capture reference, returning
// the group number and the list of transforms requested after the ':'. The
// transforms are written as "/name" segments, e.g. "1:/downcase/capitalize".
func parseGroupTemplate(inner string) (int, []string) {
	num := inner
	var transforms []string
	if i := strings.IndexByte(inner, ':'); i >= 0 {
		num = inner[:i]
		for _, t := range strings.Split(inner[i+1:], "/") {
			if t = strings.TrimSpace(t); t != "" {
				transforms = append(transforms, t)
			}
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil {
		return -1, transforms
	}
	return n, transforms
}

func captureText(line []rune, groups []oniglib.Capture, idx int, transforms []string) string {
	if idx < 0 || idx >= len(groups) {
		return ""
	}
	gp := groups[idx]
	if gp.Start < 0 || gp.End < 0 || gp.Start > gp.End || gp.End > len(line) {
		return ""
	}
	return applyTransforms(string(line[gp.Start:gp.End]), transforms)
}

func pushScope(parent []string, names ...string) []string {
	out := make([]string, len(parent), len(parent)+len(names))
	copy(out, parent)
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tokenization
// ---------------------------------------------------------------------------

type lineTokens struct {
	tokens []Token
	last   int
}

func (lt *lineTokens) produce(scopes []string, end int) {
	if end <= lt.last {
		return
	}
	// The scope slice is shared, not copied: scope lists are treated as
	// immutable (see Token.Scopes docs). This avoids an allocation per token,
	// which dominates the hot path. Many adjacent tokens share the same backing
	// slice (e.g. the stable contentScopes of the current frame).
	lt.tokens = append(lt.tokens, Token{Start: lt.last, End: end, Scopes: scopes})
	lt.last = end
}

type scanMatch struct {
	ruleID int
	isEnd  bool
	groups []oniglib.Capture
}

// TokenizeLine splits a single line (which must not contain newlines) into
// scoped tokens. Pass nil as prev for the first line of a document, then feed
// the returned state into the next call.
func (g *Grammar) TokenizeLine(line string, prev *StateStack) ([]Token, *StateStack) {
	stack := prev
	isFirstLine := prev == nil
	if stack == nil {
		stack = g.rootStack()
	}

	runes := []rune(line)
	realLen := len(runes)
	// Append a newline so that "$"/"\n" end patterns behave as in editors.
	work := make([]rune, realLen+1)
	copy(work, runes)
	work[realLen] = '\n'

	lt := &lineTokens{}
	startPos := g.checkWhileConditions(work, isFirstLine, &stack, lt)
	stack = g.tokenizeString(work, isFirstLine, lt, stack, startPos, len(work))

	tokens := clampTokens(lt.tokens, realLen)
	return tokens, stack
}

func clampTokens(in []Token, realLen int) []Token {
	out := in[:0]
	for _, t := range in {
		if t.Start >= realLen {
			continue
		}
		if t.End > realLen {
			t.End = realLen
		}
		if t.End <= t.Start {
			continue
		}
		out = append(out, t)
	}
	return out
}

// checkWhileConditions evaluates the "while" patterns of any active
// begin/while frames at the start of a line, popping frames whose condition no
// longer holds. It returns the line position to begin tokenizing from.
func (g *Grammar) checkWhileConditions(line []rune, isFirstLine bool, stack **StateStack, lt *lineTokens) int {
	// Collect while frames outermost-first.
	var frames []*StateStack
	for s := *stack; s != nil; s = s.parent {
		if s.whileRegex != nil {
			frames = append(frames, s)
		}
	}
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}

	pos := 0
	for _, f := range frames {
		caps, err := matchRegex(f.whileRegex, line, pos, isFirstLine, true)
		if err != nil || caps == nil {
			// Condition failed: pop this frame and everything above it.
			*stack = f.parent
			break
		}
		end := caps[0].End
		if end > pos {
			pos = end
		}
	}
	return pos
}

func matchRegex(re *oniglib.Regex, line []rune, pos int, allowA, allowG bool) ([]oniglib.Capture, error) {
	sc := oniglib.NewScannerFromRegexes([]*oniglib.Regex{re})
	res, err := sc.FindNextMatch(line, pos, allowA, allowG)
	if err != nil || res == nil {
		return nil, err
	}
	return res.Groups, nil
}

func (g *Grammar) tokenizeString(line []rune, isFirstLine bool, lt *lineTokens, stack *StateStack, linePos, limit int) *StateStack {
	anchorPos := -1
	maxIter := (limit+1)*64 + 1000
	iter := 0

	for {
		iter++
		if iter > maxIter {
			lt.produce(stack.contentScopes, limit)
			break
		}

		m, err := g.matchRule(line, linePos, isFirstLine, anchorPos, stack)
		if err != nil || m == nil {
			lt.produce(stack.contentScopes, limit)
			break
		}

		matchBegin := m.groups[0].Start
		matchEnd := m.groups[0].End
		if matchBegin >= limit {
			lt.produce(stack.contentScopes, limit)
			break
		}
		if matchEnd > limit {
			matchEnd = limit
		}

		lt.produce(stack.contentScopes, matchBegin)
		stackBefore := stack

		if m.isEnd {
			rule, _ := g.registry.rule(stack.rule).(*BeginEndRule)
			if rule != nil {
				g.handleCaptures(line, stack.nameScopes, rule.endCaptures, m.groups, lt)
			}
			lt.produce(stack.nameScopes, matchEnd)
			if stack.parent != nil {
				stack = stack.parent
			}
			anchorPos = matchEnd
		} else {
			switch rt := g.registry.rule(m.ruleID).(type) {
			case *BeginEndRule:
				nameScopes := pushScope(stack.contentScopes, resolveScopeName(rt.ruleName, line, m.groups))
				g.handleCaptures(line, nameScopes, rt.beginCaptures, m.groups, lt)
				lt.produce(nameScopes, matchEnd)
				contentScopes := pushScope(nameScopes, resolveScopeName(rt.contentName, line, m.groups))
				stack = &StateStack{
					parent:        stack,
					rule:          rt.id,
					endRegex:      g.resolveEndRegex(rt, line, m.groups),
					nameScopes:    nameScopes,
					contentScopes: contentScopes,
					depth:         stack.depth + 1,
				}
				anchorPos = matchEnd
			case *BeginWhileRule:
				nameScopes := pushScope(stack.contentScopes, resolveScopeName(rt.ruleName, line, m.groups))
				g.handleCaptures(line, nameScopes, rt.beginCaptures, m.groups, lt)
				lt.produce(nameScopes, matchEnd)
				contentScopes := pushScope(nameScopes, resolveScopeName(rt.contentName, line, m.groups))
				stack = &StateStack{
					parent:        stack,
					rule:          rt.id,
					whileRegex:    g.resolveWhileRegex(rt, line, m.groups),
					nameScopes:    nameScopes,
					contentScopes: contentScopes,
					depth:         stack.depth + 1,
				}
				anchorPos = matchEnd
			case *MatchRule:
				scopes := pushScope(stack.contentScopes, resolveScopeName(rt.ruleName, line, m.groups))
				g.handleCaptures(line, scopes, rt.captures, m.groups, lt)
				lt.produce(scopes, matchEnd)
				anchorPos = matchEnd
			}
		}

		if matchEnd > linePos {
			linePos = matchEnd
			isFirstLine = false
		} else if stack == stackBefore {
			// Zero-width match that did not change the stack: force progress.
			linePos++
			isFirstLine = false
		}
		if linePos >= limit {
			break
		}
	}
	return stack
}

func (g *Grammar) matchRule(line []rune, pos int, isFirstLine bool, anchorPos int, stack *StateStack) (*scanMatch, error) {
	cp := g.collectedFor(stack.rule)
	allowG := pos == anchorPos

	var inj *collectedPatterns
	if len(g.injections) > 0 {
		inj = g.injectionPatterns(stack.contentScopes)
	}
	be, isBeginEnd := g.registry.rule(stack.rule).(*BeginEndRule)

	var scanner *oniglib.Scanner
	var ruleIDs []int
	endIndex := -1

	switch {
	case inj == nil && !isBeginEnd:
		// Plain include-only / while frame: reuse the rule's cached scanner.
		scanner, ruleIDs = cp.scanner, cp.ruleIDs

	case inj == nil && isBeginEnd && !be.endHasBackRefs:
		// begin/end with a static end pattern: reuse a cached combined scanner.
		fsc := g.beScannerFor(be, cp)
		scanner, ruleIDs, endIndex = fsc.scanner, fsc.ruleIDs, fsc.endIndex

	default:
		// Dynamic end pattern (back-references) or active injections: build an
		// ephemeral scanner for this scan.
		fsc := buildFrameScanner(cp, isBeginEnd, be, stack.endRegex, stack.rule, inj)
		scanner, ruleIDs, endIndex = fsc.scanner, fsc.ruleIDs, fsc.endIndex
	}

	res, err := scanner.FindNextMatch(line, pos, isFirstLine, allowG)
	if err != nil || res == nil {
		return nil, err
	}
	return &scanMatch{
		ruleID: ruleIDs[res.PatternIndex],
		isEnd:  res.PatternIndex == endIndex,
		groups: res.Groups,
	}, nil
}

// beScannerFor returns (and caches) the combined scanner for a begin/end rule
// whose end pattern is static. The result is identical across activations of
// the rule, so it is built once.
func (g *Grammar) beScannerFor(be *BeginEndRule, cp *collectedPatterns) *frameScanner {
	g.mu.Lock()
	defer g.mu.Unlock()
	if fsc, ok := g.beCache[be.id]; ok {
		return fsc
	}
	fsc := buildFrameScanner(cp, true, be, be.endRegex, be.id, nil)
	g.beCache[be.id] = fsc
	return fsc
}

// buildFrameScanner assembles the pattern list for an active frame: the end
// pattern (for begin/end rules), the child patterns (ordered per
// applyEndPatternLast), and any injection patterns (lowest priority).
func buildFrameScanner(cp *collectedPatterns, isBeginEnd bool, be *BeginEndRule, endRegex *oniglib.Regex, endRuleID int, inj *collectedPatterns) *frameScanner {
	n := len(cp.regexes) + 1
	if inj != nil {
		n += len(inj.regexes)
	}
	regexes := make([]*oniglib.Regex, 0, n)
	ruleIDs := make([]int, 0, n)
	endIndex := -1

	addEnd := func() {
		if isBeginEnd {
			endIndex = len(regexes)
			regexes = append(regexes, endRegex)
			ruleIDs = append(ruleIDs, endRuleID)
		}
	}
	addChildren := func() {
		regexes = append(regexes, cp.regexes...)
		ruleIDs = append(ruleIDs, cp.ruleIDs...)
	}
	if isBeginEnd && be.applyEndLast {
		addChildren()
		addEnd()
	} else {
		addEnd()
		addChildren()
	}
	if inj != nil {
		regexes = append(regexes, inj.regexes...)
		ruleIDs = append(ruleIDs, inj.ruleIDs...)
	}
	return &frameScanner{
		scanner:  oniglib.NewScannerFromRegexes(regexes),
		ruleIDs:  ruleIDs,
		endIndex: endIndex,
	}
}

func (g *Grammar) resolveEndRegex(rt *BeginEndRule, line []rune, groups []oniglib.Capture) *oniglib.Regex {
	if !rt.endHasBackRefs {
		return rt.endRegex
	}
	return oniglib.NewRegex(oniglib.SubstituteBackRefs(rt.end, capturedStrings(line, groups)))
}

func (g *Grammar) resolveWhileRegex(rt *BeginWhileRule, line []rune, groups []oniglib.Capture) *oniglib.Regex {
	if !rt.whileHasBackRefs {
		return rt.whileRegex
	}
	return oniglib.NewRegex(oniglib.SubstituteBackRefs(rt.while, capturedStrings(line, groups)))
}

func capturedStrings(line []rune, groups []oniglib.Capture) []string {
	out := make([]string, len(groups))
	for i, gp := range groups {
		if gp.Start < 0 || gp.End < 0 || gp.End > len(line) || gp.Start > gp.End {
			continue
		}
		out[i] = string(line[gp.Start:gp.End])
	}
	return out
}

// handleCaptures emits tokens for the capture groups of a match, honouring
// nested capture patterns and the natural nesting of group spans.
func (g *Grammar) handleCaptures(line []rune, baseScopes []string, captures []*captureRule, groups []oniglib.Capture, lt *lineTokens) {
	if len(captures) == 0 {
		return
	}
	n := len(captures)
	if len(groups) < n {
		n = len(groups)
	}
	if n == 0 {
		return
	}

	type localEl struct {
		scopes []string
		end    int
	}
	var local []localEl
	maxEnd := groups[0].End

	curScopes := func() []string {
		if len(local) > 0 {
			return local[len(local)-1].scopes
		}
		return baseScopes
	}

	for i := 0; i < n; i++ {
		cr := captures[i]
		if cr == nil {
			continue
		}
		cap := groups[i]
		if cap.Start < 0 || cap.End < 0 {
			continue
		}
		if cap.Start > maxEnd {
			break
		}
		for len(local) > 0 && local[len(local)-1].end <= cap.Start {
			top := local[len(local)-1]
			local = local[:len(local)-1]
			lt.produce(top.scopes, top.end)
		}
		lt.produce(curScopes(), cap.Start)

		capScopes := pushScope(curScopes(), resolveScopeName(cr.scope, line, groups))
		if cr.ruleID >= 0 {
			g.tokenizeCapture(line, cap, capScopes, cr.ruleID, lt)
			lt.produce(capScopes, cap.End)
		} else if cr.scope != "" {
			local = append(local, localEl{scopes: capScopes, end: cap.End})
		}
	}
	for len(local) > 0 {
		top := local[len(local)-1]
		local = local[:len(local)-1]
		lt.produce(top.scopes, top.end)
	}
}

// tokenizeCapture re-tokenizes a captured span using a capture's nested
// patterns, producing tokens with absolute offsets into the parent line.
func (g *Grammar) tokenizeCapture(line []rune, cap oniglib.Capture, scopes []string, ruleID int, lt *lineTokens) {
	frame := &StateStack{rule: ruleID, nameScopes: scopes, contentScopes: scopes}
	g.tokenizeString(line, false, lt, frame, cap.Start, cap.End)
}
