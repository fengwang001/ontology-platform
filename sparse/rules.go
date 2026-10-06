package sparse

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"
)

// Action is whether a rule includes or excludes the paths it matches.
type Action bool

const (
	Exclude Action = false
	Include Action = true
)

func (a Action) String() string {
	if a == Include {
		return "include"
	}
	return "exclude"
}

// Rule is one ordered include/exclude rule over a path pattern.
type Rule struct {
	Action  Action
	Pattern string
}

func (r Rule) String() string { return r.Action.String() + " " + r.Pattern }

// patternKind classifies the three pattern forms.
type patternKind int8

const (
	kindExact  patternKind = iota // exact path, matches only itself
	kindPrefix                    // "dir/", matches all descendants of dir
	kindStar                      // "dir/*" (or "*"), matches direct children only
)

// parsedPattern is a validated pattern. For kindExact, segs is the full
// path; for kindPrefix and kindStar, segs is the owner directory (possibly
// empty for the root-level "*" pattern).
type parsedPattern struct {
	kind patternKind
	segs []string
}

// parsePattern validates a pattern and classifies it. Rejected: empty
// patterns, consecutive separators, ".." components, and "*" anywhere but
// as the final segment.
func parsePattern(pat string) (parsedPattern, error) {
	bad := func(reason string) (parsedPattern, error) {
		return parsedPattern{}, fmt.Errorf("%w: pattern %q: %s", ErrInvalidParam, pat, reason)
	}
	if pat == "" {
		return bad("empty pattern")
	}
	if strings.Contains(pat, "//") {
		return bad("consecutive separators")
	}
	if strings.HasSuffix(pat, "/") {
		body := pat[:len(pat)-1]
		segs, err := validPatternSegments(body)
		if err != nil {
			return bad(err.Error())
		}
		return parsedPattern{kind: kindPrefix, segs: segs}, nil
	}
	segs, err := validPatternSegments(pat)
	if err != nil {
		return bad(err.Error())
	}
	for i, s := range segs {
		if s == "*" {
			if i != len(segs)-1 {
				return bad("wildcard segment must be the final segment")
			}
			return parsedPattern{kind: kindStar, segs: segs[:len(segs)-1]}, nil
		}
	}
	return parsedPattern{kind: kindExact, segs: segs}, nil
}

func validPatternSegments(body string) ([]string, error) {
	if body == "" {
		return nil, fmt.Errorf("empty path")
	}
	segs := strings.Split(body, "/")
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("empty segment")
		}
		if s == ".." {
			return nil, fmt.Errorf("parent-directory component")
		}
	}
	return segs, nil
}

// ruleEntry is the effective (last) rule of one pattern form on one node.
type ruleEntry struct {
	include bool
	index   int // position in the ordered rule list; higher wins
}

// ruleNode is one path segment in the ruleset trie. Each node carries at
// most three effective entries: the last exact rule on this path, the last
// prefix rule on this directory, and the last star rule on this directory.
type ruleNode struct {
	children map[string]*ruleNode
	exact    *ruleEntry
	prefix   *ruleEntry
	star     *ruleEntry
	hash     uint64
}

func (n *ruleNode) child(name string) *ruleNode {
	if n == nil {
		return nil
	}
	return n.children[name]
}

func (n *ruleNode) exactEntry() *ruleEntry {
	if n == nil {
		return nil
	}
	return n.exact
}

func (n *ruleNode) prefixEntry() *ruleEntry {
	if n == nil {
		return nil
	}
	return n.prefix
}

func (n *ruleNode) starEntry() *ruleEntry {
	if n == nil {
		return nil
	}
	return n.star
}

// Ruleset is an immutable, validated, ordered rule list plus its match trie.
type Ruleset struct {
	Version int
	Rules   []Rule
	root    *ruleNode
}

// NewRuleset validates rules and builds the trie. The whole set is rejected
// if any pattern is malformed or a rule is a meaningless duplicate (same
// action and pattern as an earlier rule).
func NewRuleset(version int, rules []Rule) (*Ruleset, error) {
	rs := &Ruleset{Version: version, Rules: slices.Clone(rules), root: &ruleNode{}}
	seen := make(map[Rule]struct{}, len(rules))
	for i, r := range rules {
		pp, err := parsePattern(r.Pattern)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[r]; dup {
			return nil, fmt.Errorf("%w: duplicate rule %q", ErrInvalidParam, r)
		}
		seen[r] = struct{}{}
		n := rs.root
		for _, s := range pp.segs {
			ch := n.children[s]
			if ch == nil {
				if n.children == nil {
					n.children = map[string]*ruleNode{}
				}
				ch = &ruleNode{}
				n.children[s] = ch
			}
			n = ch
		}
		e := &ruleEntry{include: r.Action == Include, index: i}
		switch pp.kind {
		case kindExact:
			n.exact = e
		case kindPrefix:
			n.prefix = e
		case kindStar:
			n.star = e
		}
	}
	rs.root.computeHash()
	return rs, nil
}

func (n *ruleNode) computeHash() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	writeEntry := func(tag byte, e *ruleEntry) {
		h.Write([]byte{tag})
		if e == nil {
			h.Write([]byte{0})
			return
		}
		if e.include {
			h.Write([]byte{2})
		} else {
			h.Write([]byte{1})
		}
		binary.BigEndian.PutUint64(buf[:], uint64(e.index))
		h.Write(buf[:])
	}
	writeEntry('x', n.exact)
	writeEntry('p', n.prefix)
	writeEntry('s', n.star)
	for _, name := range slices.Sorted(maps.Keys(n.children)) {
		h.Write([]byte("c"))
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], n.children[name].computeHash())
		h.Write(buf[:])
	}
	n.hash = h.Sum64()
	return n.hash
}

// lastMatch finds the winning rule for a path: among the exact rule on the
// path itself, the star rule on its parent, and the prefix rules on its
// ancestors, the one with the highest rule index wins. Cost is O(depth).
func (rs *Ruleset) lastMatch(segs []string, st *Stats) (include, matched bool) {
	node := rs.root
	best := -1
	bestInc := false
	consider := func(e *ruleEntry) {
		if e != nil && e.index > best {
			best = e.index
			bestInc = e.include
		}
	}
	for i, seg := range segs {
		// node is the directory segs[:i]; the path lies strictly below it.
		consider(node.prefix)
		if i == len(segs)-1 {
			consider(node.star)
		}
		if st != nil {
			st.MatchSteps.Add(1)
		}
		node = node.child(seg)
		if node == nil {
			return bestInc, best >= 0
		}
	}
	consider(node.exact)
	return bestInc, best >= 0
}

// ruleScope is a region of the file tree whose materialization may change
// because one trie entry changed: an exact path, a prefix directory's
// descendants, or a star directory's direct children.
type ruleScope struct {
	kind patternKind
	segs []string
}

// diffRulesets emits the scopes in which oldRS and newRS disagree. Subtrees
// with identical hashes are skipped, so unchanged regions cost nothing.
func diffRulesets(oldRS, newRS *Ruleset) []ruleScope {
	var out []ruleScope
	diffRuleNodes(oldRS.root, newRS.root, nil, &out)
	return out
}

func entriesEq(a, b *ruleEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.include == b.include && a.index == b.index
}

func diffRuleNodes(a, b *ruleNode, segs []string, out *[]ruleScope) {
	if a == nil && b == nil {
		return
	}
	if a != nil && b != nil && a.hash == b.hash {
		return
	}
	emit := func(kind patternKind) {
		*out = append(*out, ruleScope{kind: kind, segs: slices.Clone(segs)})
	}
	if !entriesEq(a.exactEntry(), b.exactEntry()) {
		emit(kindExact)
	}
	if !entriesEq(a.prefixEntry(), b.prefixEntry()) {
		emit(kindPrefix)
	}
	if !entriesEq(a.starEntry(), b.starEntry()) {
		emit(kindStar)
	}
	if a != nil {
		for name, ch := range a.children {
			diffRuleNodes(ch, b.child(name), append(slices.Clone(segs), name), out)
		}
	}
	if b != nil {
		for name, ch := range b.children {
			if a.child(name) == nil {
				diffRuleNodes(nil, ch, append(slices.Clone(segs), name), out)
			}
		}
	}
}
