package rewrite

import (
	"errors"

	"ontology/route"
)

var (
	ErrInvalidFrom   = errors.New("invalid rewrite from")
	ErrInvalidTo     = errors.New("invalid rewrite to")
	ErrDuplicateRule = errors.New("duplicate rewrite rule")
)

type Rule struct {
	From  string
	To    string
	Final bool
}

type ruleNode struct {
	children map[string]*ruleNode
	rule     *Rule
}

type RuleSet struct {
	root *ruleNode
}

func NewRuleSet(rules []Rule) (*RuleSet, error) {
	ruleSet := &RuleSet{root: &ruleNode{children: make(map[string]*ruleNode)}}
	seen := make(map[string]bool)
	for _, item := range rules {
		normalizedFrom, err := route.Normalize(item.From)
		if err != nil || normalizedFrom != item.From {
			return nil, ErrInvalidFrom
		}
		if seen[item.From] {
			return nil, ErrDuplicateRule
		}
		normalizedTo, err := route.Normalize(item.To)
		if err != nil || normalizedTo != item.To {
			return nil, ErrInvalidTo
		}
		seen[item.From] = true

		node := ruleSet.root
		if item.From != "/" {
			for _, segment := range splitRuleSegments(item.From) {
				if node.children[segment] == nil {
					node.children[segment] = &ruleNode{children: make(map[string]*ruleNode)}
				}
				node = node.children[segment]
			}
		}
		rule := item
		node.rule = &rule
	}
	return ruleSet, nil
}

type Status int

const (
	StatusComplete Status = iota
	StatusHopLimit
	StatusLoop
	StatusInvalidPath
)

type Outcome struct {
	Path   string
	Hops   int
	Chain  []string
	Failed string
}

func (r *RuleSet) Rewrite(start string, maxHops int) (Outcome, Status) {
	cur := start
	seen := map[string]bool{start: true}
	chain := []string{start}
	hops := 0

	for {
		rule, rest, ok := r.lookup(cur)
		if !ok {
			return Outcome{Path: cur, Hops: hops, Chain: chain}, StatusComplete
		}
		if hops == maxHops {
			return Outcome{Path: cur, Hops: hops, Chain: chain, Failed: cur}, StatusHopLimit
		}

		target := rule.To
		if rest != "" {
			if target == "/" {
				target = rest
			} else {
				target += rest
			}
		}
		next, err := route.Normalize(target)
		if err != nil {
			return Outcome{Path: cur, Hops: hops, Chain: chain, Failed: target}, StatusInvalidPath
		}
		if seen[next] {
			return Outcome{Path: next, Hops: hops, Chain: append(chain, next), Failed: next}, StatusLoop
		}

		seen[next] = true
		hops++
		cur = next
		chain = append(chain, next)
		if rule.Final {
			return Outcome{Path: cur, Hops: hops, Chain: chain}, StatusComplete
		}
	}
}

func (r *RuleSet) lookup(path string) (Rule, string, bool) {
	node := r.root
	var best *ruleNode
	if node.rule != nil {
		best = node
	}

	for _, segment := range splitRuleSegments(path) {
		next := node.children[segment]
		if next == nil {
			break
		}
		node = next
		if node.rule != nil {
			best = node
		}
	}

	if best == nil {
		return Rule{}, "", false
	}
	prefix := best.rule.From
	rest := ""
	if prefix != "/" {
		if len(path) > len(prefix) {
			rest = path[len(prefix):]
		}
	} else if path != "/" {
		rest = path
	}
	return *best.rule, rest, true
}

func splitRuleSegments(path string) []string {
	if path == "/" {
		return nil
	}
	var segments []string
	start := 1
	for i := 1; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			segments = append(segments, path[start:i])
			start = i + 1
		}
	}
	return segments
}
