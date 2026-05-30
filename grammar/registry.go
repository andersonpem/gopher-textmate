package grammar

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/andersonpem/gopher-textmate/oniglib"
)

// Registry stores raw grammars (indexed by scope name), compiles them on
// demand, and owns the global rule-id space shared across grammars so that
// cross-grammar embedding (e.g. HTML including source.php) works seamlessly.
//
// A Registry is safe for concurrent use: compilation and rule registration are
// guarded by a mutex.
type Registry struct {
	mu       sync.Mutex
	raws     map[string]*RawGrammar
	compiled map[string]*Grammar
	rules    []Rule // global; rule id N lives at index N-1 (ids are 1-based)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		raws:     make(map[string]*RawGrammar),
		compiled: make(map[string]*Grammar),
	}
}

// AddRawGrammar registers an already-parsed grammar.
func (r *Registry) AddRawGrammar(raw *RawGrammar) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raws[raw.ScopeName] = raw
}

// AddGrammarBytes parses and registers a grammar from JSON bytes, returning its
// scope name.
func (r *Registry) AddGrammarBytes(data []byte) (string, error) {
	raw, err := ParseGrammar(data)
	if err != nil {
		return "", err
	}
	r.AddRawGrammar(raw)
	return raw.ScopeName, nil
}

// LoadGrammarFile reads, parses and registers a grammar file.
func (r *Registry) LoadGrammarFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("grammar: read %s: %w", path, err)
	}
	return r.AddGrammarBytes(data)
}

// Grammar returns the compiled grammar for scopeName, compiling it if needed.
func (r *Registry) Grammar(scopeName string) (*Grammar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.grammarLocked(scopeName)
}

func (r *Registry) grammarLocked(scopeName string) (*Grammar, error) {
	if g, ok := r.compiled[scopeName]; ok {
		return g, nil
	}
	raw, ok := r.raws[scopeName]
	if !ok {
		return nil, fmt.Errorf("grammar: no grammar registered for scope %q", scopeName)
	}
	g := newGrammar(r, raw)
	r.compiled[scopeName] = g // register before compiling root to allow self-reference

	// Reserve the root rule's id BEFORE compiling its patterns so that nested
	// "$self"/"$base" includes (which resolve to rootRuleID) see a valid id
	// during compilation rather than 0.
	rootRaw := &RawRule{Patterns: raw.Patterns}
	rootRule := &IncludeOnlyRule{}
	g.rootRuleID = r.registerRule(rootRule)
	g.ruleCache[rootRaw] = g.rootRuleID
	rootRule.patterns = g.compilePatterns(raw.Patterns)

	g.parseInjections()
	return g, nil
}

// registerRule adds a compiled rule to the global table and returns its id.
func (r *Registry) registerRule(rule Rule) int {
	r.rules = append(r.rules, rule)
	id := len(r.rules)
	switch v := rule.(type) {
	case *MatchRule:
		v.id = id
	case *BeginEndRule:
		v.id = id
	case *BeginWhileRule:
		v.id = id
	case *IncludeOnlyRule:
		v.id = id
	}
	return id
}

// rule looks up a rule by id (1-based). Returns nil for invalid ids.
func (r *Registry) rule(id int) Rule {
	if id <= 0 || id > len(r.rules) {
		return nil
	}
	return r.rules[id-1]
}

// HasGrammar reports whether a grammar with the given scope is registered.
func (r *Registry) HasGrammar(scopeName string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.raws[scopeName]
	return ok
}

// Warmup eagerly compiles every rule's regular expressions, in parallel, so
// that the expensive one-time pattern compilation happens up front (e.g. at
// application startup, ideally in a background goroutine) rather than lazily on
// the latency-sensitive tokenization path.
//
// Call it after all grammars have been compiled (e.g. via Grammar). Safe to run
// concurrently with reads, but not while grammars are still being added.
func (r *Registry) Warmup() {
	r.mu.Lock()
	rules := make([]Rule, len(r.rules))
	copy(rules, r.rules)
	r.mu.Unlock()

	regexes := make([]*oniglib.Regex, 0, len(rules))
	for _, rule := range rules {
		switch rt := rule.(type) {
		case *MatchRule:
			regexes = append(regexes, rt.match)
		case *BeginEndRule:
			regexes = append(regexes, rt.begin)
			if rt.endRegex != nil {
				regexes = append(regexes, rt.endRegex)
			}
		case *BeginWhileRule:
			regexes = append(regexes, rt.begin)
			if rt.whileRegex != nil {
				regexes = append(regexes, rt.whileRegex)
			}
		}
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(regexes) {
		workers = len(regexes)
	}
	if workers < 1 {
		return
	}
	var idx int64 = -1
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&idx, 1))
				if i >= len(regexes) {
					return
				}
				regexes[i].Warmup()
			}
		}()
	}
	wg.Wait()
}
