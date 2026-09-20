package headers

import "strings"

type entry struct {
	name   string
	values []string
}

type Set struct {
	entries []entry
	index   map[string]int
}

func (s *Set) Get(name string) (string, bool) {
	e := s.lookup(name)
	if e == nil {
		return "", false
	}
	return strings.Join(e.values, ", "), true
}

func (s *Set) Values(name string) []string {
	e := s.lookup(name)
	if e == nil {
		return nil
	}
	out := make([]string, len(e.values))
	copy(out, e.values)
	return out
}

func (s *Set) Names() []string {
	out := make([]string, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.name
	}
	return out
}

func (s *Set) lookup(name string) *entry {
	if s.index == nil {
		return nil
	}
	i, ok := s.index[canonical(name)]
	if !ok {
		return nil
	}
	return &s.entries[i]
}
