// Package rule compiles masking rules into an indexed rule set.
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// Action selects a masking method.
type Action uint8

const (
	Replace Action = iota
	Hash
	Truncate
)

// Rule is one masking rule.
type Rule struct {
	Path    string // dotted field path; empty for a value-only rule
	Pattern string // regexp applied to string values; may be empty
	Action  Action
	Keep    int // runes kept by Truncate
}

var (
	// ErrConflict reports two rules on the same path with different actions.
	ErrConflict = errors.New("rule: conflicting rules on same path")
	// ErrInvalidPattern reports an uncompilable regexp.
	ErrInvalidPattern = errors.New("rule: invalid value pattern")
)

type pattern struct {
	re     *regexp.Regexp
	action Action
	keep   int
}

type node struct {
	keys     map[string]act // exact map key actions (key may contain dots)
	children map[string]*node
}

// Node is an exported alias for a compiled path-tree node.
type Node = node

type act struct {
	action Action
	keep   int
}

// Set is a compiled rule set.
type Set struct {
	root        *node
	patterns    []pattern
	pathLookups atomic.Int64
}

// Compile validates and indexes rules.
func Compile(rules []Rule) (*Set, error) {
	s := &Set{root: &node{children: map[string]*node{}}}
	s.root.keys = map[string]act{}
	patSeen := map[string]pattern{}
	for _, r := range rules {
		if r.Path == "" && r.Pattern == "" {
			return nil, errors.New("rule: rule needs path or pattern")
		}
		if r.Action == Truncate && r.Keep <= 0 {
			return nil, fmt.Errorf("rule: truncate keep must be positive: %q", r.Path)
		}
		if r.Path != "" {
			if err := s.insert(r); err != nil {
				return nil, err
			}
		}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPattern, err)
			}
			p := pattern{re: re, action: r.Action, keep: r.Keep}
			if old, ok := patSeen[r.Pattern]; ok && old.action != r.Action {
				return nil, conflictErr(r.Pattern, old.action, r.Action)
			}
			patSeen[r.Pattern] = p
			s.patterns = append(s.patterns, p)
		}
	}
	return s, nil
}

func conflictErr(key string, a, b Action) error {
	return fmt.Errorf("%w: %s uses %s and %s", ErrConflict, key, a, b)
}

func (s *Set) insert(r Rule) error {
	segments := strings.Split(r.Path, ".")
	n := s.root
	for i, seg := range segments {
		if i == len(segments)-1 {
			break
		}
		child := n.children[seg]
		if child == nil {
			child = &node{keys: map[string]act{}, children: map[string]*node{}}
			n.children[seg] = child
		}
		n = child
	}
	key := segments[len(segments)-1]
	if old, ok := n.keys[key]; ok && old.action != r.Action {
		return conflictErr(r.Path, old.action, r.Action)
	}
	n.keys[key] = act{action: r.Action, keep: r.Keep}
	return nil
}

// Lookup resolves key at trie node n. Exact full-name keys take precedence
// over segmented descent: hit=false means apply action; when miss=true,
// descend into child (nil child means no rules live below this key).
func (s *Set) Lookup(n *node, key string) (a Action, keep int, child *node, miss bool) {
	s.pathLookups.Add(1)
	if n == nil {
		return 0, 0, nil, true
	}
	if exact, ok := n.keys[key]; ok {
		return exact.action, exact.keep, nil, false
	}
	return 0, 0, n.children[key], true
}

// Root returns the path-tree root.
func (s *Set) Root() *node { return s.root }

// MatchValue returns a matching value-pattern action.
func (s *Set) MatchValue(v string) (Action, int, bool) {
	for _, p := range s.patterns {
		if p.re.MatchString(v) {
			return p.action, p.keep, true
		}
	}
	return 0, 0, false
}

// PathLookups returns the total number of path map lookups performed.
func (s *Set) PathLookups() int64 { return s.pathLookups.Load() }

// ResetLookups zeroes the lookup counter.
func (s *Set) ResetLookups() { s.pathLookups.Store(0) }

func (a Action) String() string {
	switch a {
	case Replace:
		return "replace"
	case Hash:
		return "hash"
	default:
		return "truncate"
	}
}
