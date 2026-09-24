package find

import (
	"errors"

	"ontology/scan"
	"ontology/table"
)

var (
	ErrEmptyPattern        = errors.New("empty pattern")
	ErrPatternTooLong      = errors.New("pattern longer than configured limit")
	ErrTextShorterThanText = errors.New("pattern longer than text")
	ErrSelfCheck           = errors.New("matcher self-check failed")
)

// Matcher is an immutable compiled substring matcher.
type Matcher struct {
	table table.Table
	limit int
}

// Compile validates and compiles pattern using the given maximum length.
func Compile(pattern string, limit int) (*Matcher, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if limit > 0 && len(pattern) > limit {
		return nil, ErrPatternTooLong
	}

	t, err := table.New(pattern)
	if err != nil {
		return nil, err
	}
	return &Matcher{table: t, limit: limit}, nil
}

// FindAll returns every zero-based occurrence start, including overlaps.
func (m *Matcher) FindAll(text string) ([]int, error) {
	if len(m.table.Pattern()) > len(text) {
		return nil, ErrTextShorterThanText
	}
	scanner := scan.New(m.table)
	return append([]int(nil), scanner.Scan(text)...), nil
}

// Count returns the number of occurrences.
func (m *Matcher) Count(text string) (int, error) {
	matches, err := m.FindAll(text)
	return len(matches), err
}

// SelfCheck independently verifies the table and all reported matches.
func (m *Matcher) SelfCheck(text string) error {
	pattern := m.table.Pattern()
	for i := 0; i < m.table.Len(); i++ {
		prefixLen := m.table.At(i)
		if prefixLen < 0 || prefixLen >= i+1 {
			return ErrSelfCheck
		}
		if prefixLen > 0 && pattern[:prefixLen] != pattern[i+1-prefixLen:i+1] {
			return ErrSelfCheck
		}
	}

	matches, err := m.FindAll(text)
	if err != nil {
		return err
	}
	for _, start := range matches {
		if start < 0 || start+len(pattern) > len(text) || text[start:start+len(pattern)] != pattern {
			return ErrSelfCheck
		}
	}
	return nil
}
