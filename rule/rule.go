// Package rule compiles declarative masking rules (field paths and value
// patterns) into an O(1)-lookup set consumed by package mask.
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ontology/record"
)

// Action selects a masking transformation.
type Action string

// Supported actions.
const (
	Replace  Action = "replace"
	Hash     Action = "hash"
	Truncate Action = "truncate"
)

var (
	// ErrConflict means one path got two different actions at compile time.
	ErrConflict = errors.New("rule: conflicting actions for same path")
	// ErrInvalidPath means a path token is malformed (bad escape/array index).
	ErrInvalidPath = errors.New("rule: invalid path")
	// ErrInvalidPattern means a value regexp failed to compile.
	ErrInvalidPattern = errors.New("rule: invalid value pattern")
)

// Rule is one declarative masking rule.
type Rule struct {
	// Path is a dot-separated field path; `\.` is a literal dot, `[i]` an
	// array index. Empty for a pattern-only rule.
	Path string
	// Pattern matches string leaf values anywhere in the record (optional).
	Pattern string
	Action  Action
	// Keep is the rune count kept by truncate.
	Keep int
}

// BoundAction binds an action to one compiled path.
type BoundAction struct {
	Action Action
	Keep   int
}

// patternRule binds a compiled regexp to an action.
type patternRule struct {
	re     *regexp.Regexp
	action BoundAction
}

// Set is a compiled rule set.
type Set struct {
	byPath   map[string]BoundAction
	patterns []patternRule

	// MatchCount counts path lookups performed (non-exported via method).
	matchCount uint64
}

// ParsePath splits "a.b[0].c\.d" into segments honoring backslash escapes.
func ParsePath(p string) ([]record.Seg, error) {
	if p == "" {
		return nil, nil
	}
	var segs []record.Seg
	var key strings.Builder
	flush := func() {
		segs = append(segs, record.Seg{Key: key.String(), Index: -1})
		key.Reset()
	}
	runes := []rune(p)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
			if i >= len(runes) {
				return nil, fmt.Errorf("%w: dangling escape", ErrInvalidPath)
			}
			if runes[i] != '.' && runes[i] != '\\' {
				return nil, fmt.Errorf("%w: bad escape \\%c", ErrInvalidPath, runes[i])
			}
			key.WriteRune(runes[i])
		case '.':
			if key.Len() > 0 {
				flush()
			}
		case '[':
			end := strings.IndexRune(string(runes[i:]), ']')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated [", ErrInvalidPath)
			}
			idxText := string(runes[i+1 : i+end])
			idx, err := strconv.Atoi(idxText)
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("%w: bad index %q", ErrInvalidPath, idxText)
			}
			if key.Len() > 0 {
				flush()
			}
			segs = append(segs, record.Seg{Index: idx})
			i += end
		default:
			key.WriteRune(runes[i])
		}
	}
	flush()
	return segs, nil
}

// PathKey serializes segments to a stable map key.
func PathKey(segs []record.Seg) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteByte(0)
		}
		if s.Index >= 0 {
			fmt.Fprintf(&b, "[%d]", s.Index)
		} else {
			b.WriteString(s.Key)
		}
	}
	return b.String()
}

// Compile validates and indexes all rules.
func Compile(rules []Rule) (*Set, error) {
	s := &Set{byPath: map[string]BoundAction{}}
	for _, r := range rules {
		bound := BoundAction{Action: r.Action, Keep: r.Keep}
		switch r.Action {
		case Replace, Hash, Truncate:
		default:
			return nil, fmt.Errorf("rule: unknown action %q", r.Action)
		}
		if r.Path != "" {
			segs, err := ParsePath(r.Path)
			if err != nil {
				return nil, err
			}
			k := PathKey(segs)
			if prev, ok := s.byPath[k]; ok && prev.Action != bound.Action {
				return nil, fmt.Errorf("%w: path %q: %s vs %s",
					ErrConflict, r.Path, prev.Action, bound.Action)
			}
			s.byPath[k] = bound
		}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("%w: %q: %v", ErrInvalidPattern, r.Pattern, err)
			}
			s.patterns = append(s.patterns, patternRule{re: re, action: bound})
		}
	}
	return s, nil
}

// Lookup returns the action bound to a concrete segment path, counting one
// path match attempt regardless of hit/miss.
func (s *Set) Lookup(segs []record.Seg) (BoundAction, bool) {
	s.matchCount++
	a, ok := s.byPath[PathKey(segs)]
	return a, ok
}

// MatchPattern returns the first pattern action matching value, if any.
func (s *Set) MatchPattern(value string) (BoundAction, bool) {
	for _, p := range s.patterns {
		if p.re.MatchString(value) {
			return p.action, true
		}
	}
	return BoundAction{}, false
}

// MatchCount returns total path lookups performed so far.
func (s *Set) MatchCount() uint64 { return s.matchCount }
