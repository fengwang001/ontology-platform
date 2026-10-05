// Package owners resolves per-path code owner rules.
package owners

import "strings"

// Rule binds a path pattern to an ordered list of owners. An empty
// owner list is a valid result and means "no owners".
type Rule struct {
	Pattern string
	Owners  []string
}

// Resolver matches paths against an ordered rule list; the last
// matching rule wins.
type Resolver struct {
	rules []Rule
}

// NewResolver returns a Resolver over a copy of rules.
func NewResolver(rules []Rule) *Resolver {
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	return &Resolver{rules: cp}
}

// Match reports whether pattern matches path:
//   - "*" matches everything;
//   - a pattern ending in "/" matches paths having it as a prefix;
//   - a pattern starting with "*." matches paths ending in that suffix;
//   - anything else matches by exact equality.
func Match(pattern, path string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/"):
		return strings.HasPrefix(path, pattern)
	case strings.HasPrefix(pattern, "*."):
		return strings.HasSuffix(path, pattern[1:])
	default:
		return pattern == path
	}
}

// OwnersOf returns the owner list of the last matching rule, or nil
// when no rule matches or the matching list is empty.
func (r *Resolver) OwnersOf(path string) []string {
	var matched []string
	for _, rule := range r.rules {
		if Match(rule.Pattern, path) {
			matched = rule.Owners
		}
	}
	return matched
}
