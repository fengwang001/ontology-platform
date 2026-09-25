// Package par normalizes a large buffer in K parallel chunks with results
// byte-identical to single-threaded norm.Run, including the merged map.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

func ambiguous(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// safeCut moves a desired cut to the first non-ambiguous byte at or after it.
func safeCut(p []byte, at int) int {
	for at < len(p) && ambiguous(p[at]) {
		at++
	}
	return at
}

type seg struct {
	out []byte
	mp  *span.Map
	err error
}

// Run splits p into K chunks processed concurrently and concatenates them,
// then applies cfg's ending policy exactly once at the global end.
func Run(p []byte, k int, cfg norm.Config) ([]byte, *span.Map, error) {
	if k < 1 {
		k = 1
	}
	if k > len(p)+1 {
		k = len(p) + 1
	}
	want := make([]int, 0, k+1)
	for j := 0; j <= k; j++ {
		want = append(want, j*len(p)/k)
	}
	bounds := make([]int, 0, k+1)
	for j, at := range want {
		if j == 0 {
			bounds = append(bounds, 0)
		} else if j == len(want)-1 {
			bounds = append(bounds, len(p))
		} else {
			bounds = append(bounds, safeCut(p, at))
		}
	}
	uniq := bounds[:1]
	for _, b := range bounds[1:] {
		if b != uniq[len(uniq)-1] {
			uniq = append(uniq, b)
		}
	}
	bounds = uniq

	res := make([]seg, len(bounds)-1)
	var wg sync.WaitGroup
	for j := 0; j < len(res); j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			lo, hi := bounds[j], bounds[j+1]
			end := hi
			if hi < len(p) {
				end = hi + 1 // overlap the first non-ambiguous byte
			}
			cc := cfg
			cc.Ending = norm.Preserve
			out, mp, err := norm.Run(p[lo:end], cc)
			if err == nil && hi < end {
				mp = mp.Restrict(hi - lo)
				cut := mp.OutLen()
				out = out[:cut]
			}
			res[j] = seg{out, mp, err}
		}(j)
	}
	wg.Wait()

	var out []byte
	mp := span.New()
	for j, s := range res {
		if s.err != nil {
			return nil, nil, s.err
		}
		mp = span.Merge(mp, s.mp, bounds[j], len(out))
		out = append(out, s.out...)
	}
	if cfg.Ending != norm.Preserve {
		cut := len(out)
		for cut > 0 && out[cut-1] == '\n' {
			cut--
		}
		if cut > 0 {
			out = append(out[:cut], '\n')
		} else {
			out = out[:0]
		}
		mp = mp.RestrictOut(len(out))
	}
	mp.Finish(len(p))
	return out, mp, nil
}
