package find

import (
	"errors"

	"ontology/scan"
	"ontology/table"
)

const DefaultMaxPatternLen = 1 << 20

var (
	ErrEmptyPattern   = errors.New("empty pattern")
	ErrPatternTooLong = errors.New("pattern longer than text")
	ErrLimitExceeded  = errors.New("pattern length limit exceeded")
)

type Config struct {
	MaxPatternLen int
}

type Matcher struct {
	pattern string
	table   *table.Prefix
}

func Compile(pattern string, config Config) (*Matcher, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	limit := config.MaxPatternLen
	if limit <= 0 {
		limit = DefaultMaxPatternLen
	}
	if len(pattern) > limit {
		return nil, ErrLimitExceeded
	}
	prefix, err := table.Build(pattern)
	if err != nil {
		return nil, err
	}
	return &Matcher{pattern: pattern, table: prefix}, nil
}

func (m *Matcher) FindAll(text string) ([]int, error) {
	if len(m.pattern) > len(text) {
		return nil, ErrPatternTooLong
	}
	return scan.New(m.table).Scan(text), nil
}

func (m *Matcher) Count(text string) (int, error) {
	matches, err := m.FindAll(text)
	return len(matches), err
}

func (m *Matcher) SelfCheck(text string) bool {
	if m == nil || !m.table.SelfCheck() {
		return false
	}
	matches, err := m.FindAll(text)
	if err != nil {
		return false
	}
	for _, start := range matches {
		if start < 0 || start+len(m.pattern) > len(text) || text[start:start+len(m.pattern)] != m.pattern {
			return false
		}
	}
	return true
}
