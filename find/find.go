// Package find is the outward API: compile a pattern once, then run
// read-only searches over any number of texts.
package find

import (
	"errors"
	"fmt"

	"ontology/scan"
	"ontology/table"
)

// Sentinel errors for rejected operations; each is distinct and
// matchable with errors.Is.
var (
	ErrEmptyPattern   = errors.New("find: empty pattern")
	ErrPatternTooLong = errors.New("find: pattern longer than text")
	ErrPatternTooBig  = errors.New("find: pattern exceeds configured max length")
)

// Pattern is a compiled pattern. All its methods are read-only and
// safe for concurrent use by multiple goroutines.
type Pattern struct {
	tab     table.Table
	matcher *scan.Matcher
}

// Compile builds the failure table for pattern. maxLen is the
// configured upper bound on pattern length. A rejected compile
// returns nil and leaves no half-compiled state behind.
func Compile(pattern string, maxLen int) (*Pattern, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > maxLen {
		return nil, ErrPatternTooBig
	}
	tab := table.Compile(pattern)
	return &Pattern{tab: tab, matcher: scan.New(tab)}, nil
}

// FindAll returns every start position of the pattern in text,
// overlaps included. A rejected call changes nothing.
func (p *Pattern) FindAll(text string) ([]int, error) {
	if len(text) < p.tab.Len() {
		return nil, ErrPatternTooLong
	}
	return p.matcher.Scan(text), nil
}

// Count returns how many times the pattern occurs in text.
func (p *Pattern) Count(text string) (int, error) {
	hits, err := p.FindAll(text)
	return len(hits), err
}

// SelfCheck verifies, prefix by prefix, that every table entry is a
// proper prefix length that is also a suffix of that prefix, and that
// every position reported by FindAll matches the pattern byte for
// byte in the given text.
func (p *Pattern) SelfCheck(text string) error {
	pat := p.tab.Pattern()
	for i := 1; i <= len(pat); i++ {
		k := p.tab.At(i)
		if k < 0 || k >= i {
			return fmt.Errorf("find: table[%d]=%d is not a proper prefix length", i, k)
		}
		if pat[:k] != pat[i-k:i] {
			return fmt.Errorf("find: table[%d]=%d is not a suffix of the prefix", i, k)
		}
	}
	hits, err := p.FindAll(text)
	if err != nil {
		return err
	}
	for _, pos := range hits {
		if text[pos:pos+len(pat)] != pat {
			return fmt.Errorf("find: reported position %d does not match", pos)
		}
	}
	return nil
}
