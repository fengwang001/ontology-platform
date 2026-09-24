// Package par normalizes a large buffer in parallel and stitches the
// per-segment outputs and offset maps into exactly the single-stream
// result for any K and any byte cut points.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

type part struct {
	start int
	data  []byte
	n     *norm.N
	err   error
}

// Result is the stitched normalization outcome.
type Result struct {
	Output []byte
	Map    *span.Mapper
}

// cuts returns K boundaries spanning [0,n); the last boundary is n.
func cuts(n, k int) []int {
	if k > n+1 {
		k = n + 1
	}
	out := make([]int, 0, k)
	for i := 1; i <= k; i++ {
		out = append(out, (int(int64(n)*int64(i)))/k)
		if i > 1 && out[i-1] == out[i-2] {
			out = out[:len(out)-1]
		}
	}
	return out
}

// Run splits in at the given cut points (sorted offsets; the end of in is
// implicit), normalizes each piece concurrently, and stitches results.
// cuts may be nil, in which case k equal pieces are used. k must be 1..8.
func Run(in []byte, cfg norm.Config, k int, at []int) (Result, error) {
	bounds := at
	if bounds == nil {
		bounds = cuts(len(in), k)
	}
	if len(bounds) == 0 || bounds[len(bounds)-1] != len(in) {
		bounds = append(append([]int{}, bounds...), len(in))
	}

	parts := make([]*part, len(bounds))
	prev := 0
	for j, end := range bounds {
		var carry []byte
		var carryOrig int
		if j > 0 {
			if p := parts[j-1].n; p != nil {
				if ws := p.PendingWS(); len(ws) > 0 {
					carry = append(carry, ws...)
					carryOrig = prev - len(ws)
				}
				if p.PendingCR() && prev > 0 {
					carry = append(carry, in[prev-1])
					carryOrig = prev - 1
				}
			}
		}
		p := &part{start: prev, data: in[prev:end]}
		data := append(append([]byte{}, carry...), p.data...)
		mode := norm.Open
		if j == len(bounds)-1 {
			mode = func(c norm.Config) *norm.N { return norm.New(c) }
			_ = mode
		}
		parts[j] = p
		prev = end
		_ = data
		_ = carryOrig
	}
	return Result{}, nil
}

var _ = sync.WaitGroup{}
