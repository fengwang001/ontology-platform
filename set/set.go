// Package set holds an ordered include/exclude rule set with
// last-match-wins semantics and atomic hot replacement.
package set

import (
	"errors"
	"strings"
	"sync/atomic"

	"ontology/engine"
	"ontology/syntax"
)

// Verdict is the conclusion for a path.
type Verdict int

const (
	Undecided Verdict = iota // no rule matched
	Included
	Excluded
)

// Options configures limits; zero values use defaults.
type Options struct {
	MaxPatternBytes int
	MaxDoubleStar   int
	MaxRules        int
}

var ErrTooManyRules = errors.New("rule set exceeds rule limit")

// Decision is the outcome of Explain, fully from one version.
type Decision struct {
	Verdict Verdict
	Rule    string // raw text of the deciding rule
	Index   int    // index of the deciding rule, -1 if undecided
	Version int    // rule set version that produced this decision
}

type rule struct {
	raw     string
	exclude bool
	pat     *engine.Pattern
}

type version struct {
	id    int
	rules []rule
}

// Set is a concurrently queryable, atomically replaceable rule set.
type Set struct {
	lim      syntax.Limits
	maxRules int
	seq      atomic.Int64
	cur      atomic.Pointer[version]
}

// New returns an empty Set (all paths Undecided, version 0).
func New(opt Options) *Set {
	if opt.MaxRules <= 0 {
		opt.MaxRules = 1024
	}
	s := &Set{
		lim:      syntax.Limits{MaxBytes: opt.MaxPatternBytes, MaxDouble: opt.MaxDoubleStar},
		maxRules: opt.MaxRules,
	}
	s.cur.Store(&version{})
	return s
}

// Replace compiles all rules and swaps them in atomically. On any
// error the old rule set is kept untouched.
func (s *Set) Replace(rules []string) error {
	if len(rules) > s.maxRules {
		return ErrTooManyRules
	}
	v := &version{}
	for _, r := range rules {
		rl := rule{raw: r}
		pat := r
		if strings.HasPrefix(r, "!") {
			rl.exclude = true
			pat = r[1:]
		}
		p, err := engine.Compile(pat, s.lim)
		if err != nil {
			return err
		}
		rl.pat = p
		v.rules = append(v.rules, rl)
	}
	v.id = int(s.seq.Add(1))
	s.cur.Store(v)
	return nil
}

// Explain returns the decision for path, taken entirely from one
// version of the rule set; the last matching rule decides.
func (s *Set) Explain(path string) Decision {
	v := s.cur.Load()
	d := Decision{Verdict: Undecided, Index: -1, Version: v.id}
	for i, r := range v.rules {
		if r.pat.Match(path) {
			d.Verdict = Included
			if r.exclude {
				d.Verdict = Excluded
			}
			d.Rule = r.raw
			d.Index = i
		}
	}
	return d
}

// Query returns just the verdict for path.
func (s *Set) Query(path string) Verdict { return s.Explain(path).Verdict }
