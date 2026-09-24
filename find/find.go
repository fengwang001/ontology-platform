// Package find is the public facade: compile a pattern once, then run
// read-only searches over any number of texts, concurrently.
package find

import (
	"errors"

	"ontology/scan"
	"ontology/table"
)

var (
	ErrEmptyPattern   = errors.New("find: empty pattern")
	ErrPatternTooLong = errors.New("find: pattern longer than text")
	ErrExceedsLimit   = errors.New("find: pattern exceeds configured limit")
	ErrBrokenTable    = errors.New("find: failure table self-check failed")
	ErrBogusMatch     = errors.New("find: reported position is not a match")
)

// DefaultMaxPatternLen applies when Compile gets a non-positive limit.
const DefaultMaxPatternLen = 1 << 20

// Matcher is a compiled pattern. It is immutable after Compile and safe
// for concurrent use.
type Matcher struct {
	pat string
	tab table.Table
}

// Compile validates pattern and builds its failure table. Any rejection
// returns a nil Matcher and leaves no half-compiled state behind.
func Compile(pattern string, maxLen int) (*Matcher, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if maxLen <= 0 {
		maxLen = DefaultMaxPatternLen
	}
	if len(pattern) > maxLen {
		return nil, ErrExceedsLimit
	}
	return &Matcher{pat: pattern, tab: table.Build(pattern)}, nil
}

// FindAll returns every start position where the pattern occurs in text,
// including overlapping occurrences.
func (m *Matcher) FindAll(text string) ([]int, error) {
	if len(m.pat) > len(text) {
		return nil, ErrPatternTooLong
	}
	return scan.New(m.pat, m.tab).Scan(text), nil
}

// Count returns the number of occurrences of the pattern in text.
func (m *Matcher) Count(text string) (int, error) {
	hits, err := m.FindAll(text)
	return len(hits), err
}

// SelfCheck verifies the failure table position by position (each value
// is a proper-prefix length whose prefix is also a suffix of that prefix)
// and confirms every position reported by FindAll on text matches the
// pattern byte for byte.
func (m *Matcher) SelfCheck(text string) error {
	for k := 0; k < m.tab.Len(); k++ {
		v := m.tab.At(k)
		if v < 0 || v >= k+1 || m.pat[:v] != m.pat[k+1-v:k+1] {
			return ErrBrokenTable
		}
	}
	hits, err := m.FindAll(text)
	if err != nil {
		return err
	}
	for _, h := range hits {
		for j := 0; j < len(m.pat); j++ {
			if text[h+j] != m.pat[j] {
				return ErrBogusMatch
			}
		}
	}
	return nil
}
