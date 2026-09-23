// Package set holds an ordered include/exclude rule set. The last
// matching rule decides; queries see one whole immutable version and
// replacement is atomic: a failed compile keeps the old version.
package set

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/engine"
	"ontology/syntax"
)

var ErrTooManyRules = errors.New("rule count exceeds limit")

const DefaultMaxRules = 1024

// Verdict is the outcome for one path.
type Verdict int

const (
	Undecided Verdict = iota // no rule matched
	Include
	Exclude
)

func (v Verdict) String() string {
	return []string{"undecided", "include", "exclude"}[v]
}

// Decision explains one query: the verdict plus the deciding rule's
// raw text, index, and the version they came from.
type Decision struct {
	Verdict Verdict
	Raw     string // raw rule text; "" when undecided
	Index   int    // rule index; -1 when undecided
	Version uint64
}

type compiled struct {
	include bool
	raw     string
	pat     *syntax.Pattern
}

type version struct {
	id    uint64
	rules []compiled
}

// Set is a hot-swappable rule set safe for concurrent queries.
type Set struct {
	cur      atomic.Pointer[version]
	seq      atomic.Uint64
	mu       sync.Mutex // serializes Replace
	lim      syntax.Limits
	maxRules int
}

// New builds a Set; maxRules <= 0 selects DefaultMaxRules.
func New(rules []string, lim syntax.Limits, maxRules int) (*Set, error) {
	if maxRules <= 0 {
		maxRules = DefaultMaxRules
	}
	s := &Set{lim: lim, maxRules: maxRules}
	if err := s.Replace(rules); err != nil {
		return nil, err
	}
	return s, nil
}

// Replace compiles rules and swaps them in atomically. On any compile
// error the old version stays in effect. A leading '!' marks exclude.
func (s *Set) Replace(rules []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(rules) > s.maxRules {
		return ErrTooManyRules
	}
	cr := make([]compiled, len(rules))
	for i, raw := range rules {
		pat, include := raw, true
		if strings.HasPrefix(pat, "!") {
			pat, include = pat[1:], false
		}
		p, err := syntax.Compile(pat, s.lim)
		if err != nil {
			return err
		}
		cr[i] = compiled{include: include, raw: raw, pat: p}
	}
	s.cur.Store(&version{id: s.seq.Add(1), rules: cr})
	return nil
}

// Version returns the id of the currently active rule version.
func (s *Set) Version() uint64 { return s.cur.Load().id }

// Explain returns the verdict and the deciding rule, all taken from a
// single load of the current version.
func (s *Set) Explain(path string) Decision {
	v := s.cur.Load()
	d := Decision{Verdict: Undecided, Index: -1, Version: v.id}
	for i, r := range v.rules {
		if engine.Match(r.pat, path) {
			d.Verdict, d.Raw, d.Index = Include, r.raw, i
			if !r.include {
				d.Verdict = Exclude
			}
		}
	}
	return d
}

// Decide returns only the verdict for path.
func (s *Set) Decide(path string) Verdict { return s.Explain(path).Verdict }
