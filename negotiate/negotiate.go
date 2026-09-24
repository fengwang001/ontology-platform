// Package negotiate selects a response media type for an Accept header.
package negotiate

import (
	"errors"

	"ontology/mtype"
	"ontology/rank"
)

// ErrNotAcceptable is returned when no candidate is acceptable; there is
// never a fallback to the first candidate.
var ErrNotAcceptable = errors.New("no acceptable media type")

// Select picks a candidate for the Accept header. A nil header means the
// header is absent, equivalent to */*. A non-nil empty header means the
// client accepts nothing. Invalid accept items are dropped (see DESIGN.md);
// ties are broken by q, then specificity, then candidate-list order.
func Select(accept *string, candidates []string) (string, error) {
	header := "*/*"
	if accept != nil {
		header = *accept
	}
	items, _ := mtype.Parse(header)
	bestQ, bestSpec, best := -1, -1, ""
	for _, c := range candidates {
		mt, err := mtype.ParseOne(c)
		if err != nil {
			return "", err
		}
		q, spec := quality(mt, items)
		if q > bestQ || q == bestQ && spec > bestSpec {
			bestQ, bestSpec, best = q, spec, c
		}
	}
	if bestQ <= 0 {
		return "", ErrNotAcceptable
	}
	return best, nil
}

// quality returns the q and specificity a candidate gets from the accept
// items: the matching item with the highest specificity wins (RFC 7231),
// so an explicit q=0 rejects the type even if a wildcard allows it.
func quality(c mtype.Type, items []mtype.Type) (int, int) {
	bestQ, bestSpec := 0, -1
	for _, it := range items {
		if spec, ok := rank.Score(c, it); ok && (spec > bestSpec || spec == bestSpec && it.Q > bestQ) {
			bestSpec, bestQ = spec, it.Q
		}
	}
	return bestQ, bestSpec
}
