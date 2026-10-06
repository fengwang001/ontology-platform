package apf

import "sort"

// compiledRule is a Rule preprocessed for matching: sets become maps and the
// slice is sorted by (Precedence, Name).
type compiledRule struct {
	name          string
	precedence    int
	users         map[string]bool
	verbs         map[string]bool
	resources     map[string]bool
	level         string
	distinguishBy Distinguisher
}

func toSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func compileRules(rules []Rule) []compiledRule {
	compiled := make([]compiledRule, len(rules))
	for i, r := range rules {
		compiled[i] = compiledRule{
			name:          r.Name,
			precedence:    r.Precedence,
			users:         toSet(r.Users),
			verbs:         toSet(r.Verbs),
			resources:     toSet(r.Resources),
			level:         r.Level,
			distinguishBy: r.DistinguishBy,
		}
	}
	sort.SliceStable(compiled, func(i, j int) bool {
		if compiled[i].precedence != compiled[j].precedence {
			return compiled[i].precedence < compiled[j].precedence
		}
		return compiled[i].name < compiled[j].name
	})
	return compiled
}

func (r *compiledRule) matches(req Request) bool {
	if r.users != nil && !r.users[req.User] {
		return false
	}
	if r.verbs != nil && !r.verbs[req.Verb] {
		return false
	}
	if r.resources != nil && !r.resources[req.Resource] {
		return false
	}
	return true
}

// classify returns the level name and flow key for req. ok is false when no
// rule matches.
func classify(rules []compiledRule, req Request) (level string, flow string, ok bool) {
	for i := range rules {
		r := &rules[i]
		if !r.matches(req) {
			continue
		}
		if r.distinguishBy == ByNamespace {
			return r.level, "ns:" + req.Namespace, true
		}
		return r.level, "user:" + req.User, true
	}
	return "", "", false
}
