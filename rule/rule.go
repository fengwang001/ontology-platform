// Package rule compiles masking rules (by field path, by value pattern)
// into an immutable set with O(1) per-node path lookup.
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// Action selects how a matched value is masked.
type Action int

const (
	ActionReplace Action = iota
	ActionHash
	ActionTruncate
)

func (a Action) String() string {
	switch a {
	case ActionReplace:
		return "replace"
	case ActionHash:
		return "hash"
	case ActionTruncate:
		return "truncate"
	}
	return "unknown"
}

// ErrConflict reports contradictory rules (same path, different actions).
var ErrConflict = errors.New("rule: conflicting rules")

// Rule masks values either at a field path or wherever a value pattern
// matches. Path uses dot-separated segments; quote segments containing
// dots with double quotes (`"a.b".c`). Param is the replacement text for
// ActionReplace or the keep-length (decimal) for ActionTruncate.
type Rule struct {
	Path    string
	Pattern string
	Action  Action
	Param   string
}

// PatternRule is a compiled value-pattern rule.
type PatternRule struct {
	Re     *regexp.Regexp
	Action Action
	Param  string
}

type pathRule struct {
	action Action
	param  string
}

// Set is a compiled, immutable rule set. Safe for concurrent use.
type Set struct {
	paths    map[string]pathRule
	patterns []PatternRule
	size     int
	matches  atomic.Int64
}

// Compile validates rules and precompiles them for O(1) path lookup.
func Compile(rules []Rule) (*Set, error) {
	s := &Set{paths: map[string]pathRule{}}
	firstIdx := map[string]int{}
	for i, r := range rules {
		if r.Path == "" && r.Pattern == "" {
			return nil, fmt.Errorf("rule: rule #%d has neither path nor pattern", i)
		}
		if r.Path != "" {
			segs, err := ParsePath(r.Path)
			if err != nil {
				return nil, fmt.Errorf("rule: rule #%d: %w", i, err)
			}
			key := Join(segs)
			if prev, ok := s.paths[key]; ok {
				if prev.action != r.Action || prev.param != r.Param {
					return nil, fmt.Errorf("%w: path %q: rule #%d (%s) vs rule #%d (%s)",
						ErrConflict, r.Path, firstIdx[key], prev.action, i, r.Action)
				}
			} else {
				firstIdx[key] = i
				s.paths[key] = pathRule{action: r.Action, param: r.Param}
			}
		}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule: rule #%d: bad pattern: %w", i, err)
			}
			s.patterns = append(s.patterns, PatternRule{Re: re, Action: r.Action, Param: r.Param})
		}
		s.size++
	}
	return s, nil
}

// ParsePath splits a rule path into segments, honoring double quotes.
func ParsePath(s string) ([]string, error) {
	if s == "" {
		return nil, errors.New("empty path")
	}
	var segs []string
	i := 0
	for i < len(s) {
		if s[i] == '"' {
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("unterminated quote in %q", s)
			}
			segs = append(segs, s[i+1:i+1+j])
			i += j + 2
		} else {
			j := strings.IndexByte(s[i:], '.')
			if j < 0 {
				segs = append(segs, s[i:])
				i = len(s)
			} else {
				if j == 0 {
					return nil, fmt.Errorf("empty segment in %q", s)
				}
				segs = append(segs, s[i:i+j])
				i += j
			}
		}
		if i == len(s) {
			break
		}
		if s[i] != '.' {
			return nil, fmt.Errorf("expected '.' after segment in %q", s)
		}
		i++
		if i == len(s) {
			return nil, fmt.Errorf("trailing '.' in %q", s)
		}
	}
	return segs, nil
}

// Join renders segments in canonical form, quoting segments that contain
// dots or quotes. It is the inverse of ParsePath.
func Join(segs []string) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteByte('.')
		}
		if s == "" || strings.ContainsAny(s, ".\"") {
			b.WriteByte('"')
			b.WriteString(s)
			b.WriteByte('"')
		} else {
			b.WriteString(s)
		}
	}
	return b.String()
}

// Lookup resolves a canonical field path. Each call counts as one path
// match; see MatchCount.
func (s *Set) Lookup(canonical string) (Action, string, bool) {
	s.matches.Add(1)
	pr, ok := s.paths[canonical]
	return pr.action, pr.param, ok
}

// Patterns returns the compiled value-pattern rules.
func (s *Set) Patterns() []PatternRule { return s.patterns }

// Size returns the number of compiled rules.
func (s *Set) Size() int { return s.size }

// MatchCount is the total number of path lookups performed.
func (s *Set) MatchCount() int64 { return s.matches.Load() }
