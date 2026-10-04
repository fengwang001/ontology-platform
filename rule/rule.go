// Package rule implements descriptor-pattern matching and
// most-specific-rule selection. It is pure: it holds no state.
package rule

import (
	"sort"
	"strconv"
	"strings"
)

// Wildcard marks a pattern value that matches any present key.
const Wildcard = "*"

// KV is a single key/value pair of a descriptor or a pattern.
type KV struct {
	Key   string
	Value string
}

// Pattern is a canonicalized match pattern: pairs sorted by key.
type Pattern struct {
	pairs []KV
}

// NewPattern canonicalizes pairs by sorting them by key. The caller is
// responsible for validating arity, non-emptiness and key distinctness.
func NewPattern(pairs []KV) Pattern {
	cp := make([]KV, len(pairs))
	copy(cp, pairs)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Key < cp[j].Key })
	return Pattern{pairs: cp}
}

// Pairs returns the canonical (key-sorted) pairs.
func (p Pattern) Pairs() []KV {
	out := make([]KV, len(p.pairs))
	copy(out, p.pairs)
	return out
}

// Exact counts the non-wildcard pairs.
func (p Pattern) Exact() int {
	n := 0
	for _, kv := range p.pairs {
		if kv.Value != Wildcard {
			n++
		}
	}
	return n
}

// Match reports whether every pattern key is present in desc with an equal
// value; a "*" value only requires the key to be present.
func (p Pattern) Match(desc map[string]string) bool {
	for _, kv := range p.pairs {
		v, ok := desc[kv.Key]
		if !ok {
			return false
		}
		if kv.Value != Wildcard && kv.Value != v {
			return false
		}
	}
	return true
}

// encode writes s in an unambiguous length-prefixed form.
func encode(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
	b.WriteByte(';')
}

// keySet encodes the sorted key list; two patterns share a key set iff
// their encodings are equal.
func (p Pattern) keySet() string {
	var b strings.Builder
	for _, kv := range p.pairs {
		encode(&b, kv.Key)
	}
	return b.String()
}

// Signature encodes the full pair set (keys and values, including "*"
// positions); two patterns are identical iff their signatures are equal.
func (p Pattern) Signature() string {
	var b strings.Builder
	for _, kv := range p.pairs {
		encode(&b, kv.Key)
		encode(&b, kv.Value)
	}
	return b.String()
}

// ValuesKey encodes the actual descriptor values for the pattern keys, in
// canonical key order. Together with a rule id it identifies a counter.
func (p Pattern) ValuesKey(desc map[string]string) string {
	var b strings.Builder
	for _, kv := range p.pairs {
		encode(&b, desc[kv.Key])
	}
	return b.String()
}

// Rule couples a pattern with its id for selection.
type Rule struct {
	ID      string
	Pattern Pattern
}

// Select groups matching rules by key set, keeps within each group only
// the rules with the most exact (non-"*") pairs, breaks ties by smallest
// id in byte order, and stacks the winners across key sets. The result is
// sorted by id in byte order.
func Select(rules []Rule) []Rule {
	best := map[string]Rule{}
	bestExact := map[string]int{}
	for _, r := range rules {
		ks := r.Pattern.keySet()
		ex := r.Pattern.Exact()
		cur, ok := best[ks]
		if !ok || ex > bestExact[ks] || (ex == bestExact[ks] && r.ID < cur.ID) {
			best[ks] = r
			bestExact[ks] = ex
		}
	}
	out := make([]Rule, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
