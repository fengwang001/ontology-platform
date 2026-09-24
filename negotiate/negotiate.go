// Package negotiate selects the best offered media type for an Accept
// header, or reports that nothing is acceptable.
package negotiate

import (
	"errors"

	"ontology/mtype"
	"ontology/rank"
)

// ErrUnacceptable reports that no offered type is acceptable. It is a
// definite result, never a fallback to the first candidate.
var ErrUnacceptable = errors.New("negotiate: no acceptable media type")

// Select picks the best offered type for the Accept header.
// A nil accept means the header is missing (equivalent to "*/*");
// an empty header accepts nothing. Scores compare q first, then
// specificity; full ties keep the earlier offered type. Inputs are
// never mutated and results are deterministic.
func Select(accept *string, offered []string) (string, error) {
	header := "*/*"
	if accept != nil {
		header = *accept
	}
	if header == "" {
		return "", ErrUnacceptable
	}
	items, err := rank.ParseAccept(header)
	if err != nil {
		return "", err
	}
	best, bestQ, bestSpec := "", 0, 0
	for _, cand := range offered {
		mt, err := mtype.Parse(cand, -1)
		if err != nil {
			return "", err
		}
		q, spec := 0, 0
		for _, it := range items {
			if sp, ok := rank.Match(mt, it); ok && (it.Q > q || it.Q == q && sp > spec) {
				q, spec = it.Q, sp
			}
		}
		if q > 0 && (q > bestQ || q == bestQ && spec > bestSpec) {
			best, bestQ, bestSpec = cand, q, spec
		}
	}
	if bestQ == 0 {
		return "", ErrUnacceptable
	}
	return best, nil
}
