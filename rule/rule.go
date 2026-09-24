package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Action string

const (
	Replace  Action = "replace"
	Hash     Action = "hash"
	Truncate Action = "truncate"
)

type Rule struct {
	Name        string
	Path        string
	Value       string
	Action      Action
	Keep        int
	Replacement string
}

type Compiled struct {
	name   string
	action Action
	keep   int
	repl   string
}

type valueRule struct {
	Compiled
	pattern *regexp.Regexp
}

type Set struct {
	exact    map[string]Compiled
	nested   map[string]map[string]Compiled
	shadows  map[string]map[string]map[string]bool
	values   []valueRule
	pathHits uint64
}

var ErrConflict = errors.New("rule: conflicting masking rules")

func Compile(rules []Rule) (*Set, error) {
	set := &Set{exact: map[string]Compiled{}, nested: map[string]map[string]Compiled{}, shadows: map[string]map[string]map[string]bool{}}
	literalPaths := map[string]bool{}
	for _, source := range rules {
		compiled := Compiled{source.Name, source.Action, source.Keep, source.Replacement}
		if source.Action != Replace && source.Action != Hash && source.Action != Truncate {
			return nil, fmt.Errorf("rule %q: unknown action %q", source.Name, source.Action)
		}
		if source.Path == "" {
			pattern, err := regexp.Compile(source.Value)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", source.Name, err)
			}
			set.values = append(set.values, valueRule{compiled, pattern})
			continue
		}
		if existing, ok := set.exact[source.Path]; ok && existing.action != source.Action {
			return nil, fmt.Errorf("%w: %s(%s) vs %s(%s) for path %q",
				ErrConflict, existing.name, existing.action, source.Name, source.Action, source.Path)
		}
		set.exact[source.Path] = compiled
		literalPaths[source.Path] = true
		parts := strings.Split(source.Path, ".")
		if len(parts) > 1 {
			parent := strings.Join(parts[:len(parts)-1], ".")
			leaf := parts[len(parts)-1]
			if set.nested[parent] == nil {
				set.nested[parent] = map[string]Compiled{}
			}
			set.nested[parent][leaf] = compiled
		}
	}
	for path := range literalPaths {
		parts := strings.Split(path, ".")
		if len(parts) < 2 {
			continue
		}
		parent := strings.Join(parts[:len(parts)-1], ".")
		key := parts[len(parts)-1]
		if set.shadows[""] == nil {
			set.shadows[""] = map[string]map[string]bool{}
		}
		if set.shadows[""][parts[0]] == nil {
			set.shadows[""][parts[0]] = map[string]bool{}
		}
		if set.shadows[parent] == nil {
			set.shadows[parent] = map[string]map[string]bool{}
		}
		set.shadows[parent][key] = map[string]bool{}
	}
	return set, nil
}

func (s *Set) RulesAt(prefix string) (map[string]Compiled, map[string]map[string]bool) {
	s.pathHits++
	if prefix == "" {
		return s.exact, s.shadows[""]
	}
	return s.nested[prefix], s.shadows[prefix]
}

func (s *Set) MatchValues(value string) []Compiled {
	var matched []Compiled
	for _, candidate := range s.values {
		s.pathHits++
		if candidate.pattern.MatchString(value) {
			matched = append(matched, candidate.Compiled)
		}
	}
	return matched
}

func (s *Set) MatchCount() uint64 { return s.pathHits }

func (r Compiled) Action() Action      { return r.action }
func (r Compiled) Name() string        { return r.name }
func (r Compiled) Keep() int           { return r.keep }
func (r Compiled) Replacement() string { return r.repl }
