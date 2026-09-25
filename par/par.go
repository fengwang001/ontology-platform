// Package par normalizes one buffer split at arbitrary byte offsets into K
// parallel stage normalizers, then sequentially repairs segment-boundary
// ambiguity. Output and the composed map are byte-identical to single-stream
// normalization for every K and every set of cut points.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

type result struct {
	n   *norm.Normalizer
	err error
}

// Run splits data into k segments, normalizes concurrently with cfg (Ending is
// applied to the final segment only), and returns output and the global map.
func Run(data []byte, k int, cfg norm.Config) ([]byte, *span.Map, error) {
	if k < 1 {
		k = 1
	}
	if k > len(data) && len(data) > 0 {
		k = len(data)
	}
	bounds := bounds(len(data), k)
	res := make([]result, k)
	var wg sync.WaitGroup
	for si := 0; si < k; si++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			c := cfg
			c.Stage = true
			c.StartOrig = bounds[si]
			n := norm.New(c)
			_, err := n.Write(data[bounds[si]:bounds[si+1]])
			if err == nil {
				err = n.Close()
			}
			res[si] = result{n: n, err: err}
		}(si)
	}
	wg.Wait()

	var out []byte
	gm := span.New(0)
	var carry []byte
	carryBase := 0
	for si := 0; si < k; si++ {
		r := res[si]
		if r.err != nil {
			return out, gm, r.err // global offset already; prior output kept
		}
		var cur *norm.Normalizer
		if len(carry) == 0 {
			out = append(out, r.n.Output()...)
			gm.Append(r.n.Map(), 0)
			cur = r.n
		} else {
			// Redo this boundary starting with the prior carry; the result
			// replaces this segment's worker output for the fold.
			cur = fold(cfg, carry, carryBase, data[bounds[si]:bounds[si+1]], si == k-1)
			out = append(out, cur.Output()...)
			gm.Append(cur.Map(), 0)
		}
		c, base := cur.Carry()
		if len(c) > 0 {
			carry, carryBase = c, base
		} else {
			carry = nil
		}
	}
	if len(carry) > 0 { // pending bytes of the last segment
		fn := tail(cfg, carry, carryBase)
		out = append(out, fn.Output()...)
		for _, rg := range fn.Map().Ranges() {
			gm.Delete(rg[0], rg[1])
		}
	}
	out = applyEnding(cfg.Ending, out)
	gm.Finish(len(data), len(out))
	return out, gm, nil
}

func applyEnding(e norm.Ending, out []byte) []byte {
	switch e {
	case norm.EnsureOne:
		if len(out) == 0 {
			return out
		}
		k := len(out)
		for k > 0 && out[k-1] == '\n' {
			k--
		}
		if k == 0 {
			return append(out[:0], '\n')
		}
		return append(out[:k], '\n')
	case norm.TrimEmpty:
		k := len(out)
		for k > 0 && out[k-1] == '\n' {
			k--
		}
		if k > 0 {
			return append(out[:k], '\n')
		}
		return out[:0]
	}
	return out
}

func bounds(n, k int) []int {
	b := make([]int, k+1)
	for i := 0; i <= k; i++ {
		b[i] = n * i / k
	}
	return b
}

func fold(cfg norm.Config, carry []byte, carryBase int, seg []byte, last bool) *norm.Normalizer {
	c := cfg
	c.Stage = true // global policy applied once after the fold in Run
	c.StartOrig = carryBase
	n := norm.New(c)
	_, _ = n.Write(carry)
	_, _ = n.Write(seg)
	_ = n.Close()
	return n
}

func tail(cfg norm.Config, carry []byte, carryBase int) *norm.Normalizer {
	c := cfg
	c.Stage = true // global policy is applied once by Run
	c.StartOrig = carryBase
	n := norm.New(c)
	_, _ = n.Write(carry)
	_ = n.Close()
	return n
}
