// Package par splits a large buffer at byte offsets, normalizes K segments
// concurrently, and concatenates their output and offset maps so the result is
// byte-identical to single-stream normalization.
package par

import (
	"ontology/norm"
	"ontology/span"
	"ontology/ws"
)

type segResult struct {
	out []byte
	tab *span.Table
	err error
}

// snap moves a cut leftward so it never divides a trailing-whitespace run or a
// '\r' from its successor '\n'. Cuts are then forced monotone.
func snap(data string, cut, prev int) int {
	b := cut
	for b > 0 && b > prev && ws.IsSpace(data[b-1]) {
		b--
	}
	if b > 0 && b > prev && data[b-1] == '\r' {
		b--
	}
	if b < prev {
		b = prev
	}
	return b
}

// boundaries returns K+1 boundaries derived from K-way split points. Each cut
// is snapped left over whitespace and a possible '\r', then forced strictly
// increasing (so one huge whitespace run makes only one of its cuts survive).
func boundaries(data string, k int) []int {
	if k < 1 {
		k = 1
	}
	if k > len(data)+1 {
		k = len(data) + 1
	}
	bounds := make([]int, k+1)
	bounds[0], bounds[k] = 0, len(data)
	for j := 1; j < k; j++ {
		bounds[j] = snap(data, j*len(data)/k, 0)
	}
	for j := 1; j < k; j++ {
		min := bounds[j-1]
		if bounds[j] < min {
			bounds[j] = min
		}
	}
	for j := k - 1; j >= 1; j-- {
		max := bounds[j+1]
		if bounds[j] > max {
			bounds[j] = max
		}
	}
	return bounds
}

// Normalize processes data as k concurrent segments with the given norm config.
// It returns concatenated output and a globally shifted/merged offset table.
func Normalize(data []byte, k int, cfg norm.Config) ([]byte, *span.Table, error) {
	bounds := boundaries(string(data), k)
	nseg := len(bounds) - 1
	res := make([]segResult, nseg)
	done := make(chan int, nseg)
	for j := 0; j < nseg; j++ {
		go func(j int) {
			a, b := bounds[j], bounds[j+1]
			if a == b {
				done <- j
				return
			}
			n := norm.New(cfg)
			_, err := n.Write(data[a:b])
			if err == nil {
				if j == nseg-1 {
					err = n.Close()
				} else {
					n.CloseInterior()
				}
			}
			if err != nil {
				res[j] = segResult{err: err}
			} else {
				res[j] = segResult{out: n.Output(), tab: n.Map()}
			}
			done <- j
		}(j)
	}
	for range nseg {
		j := <-done
		if res[j].err != nil {
			return nil, nil, res[j].err
		}
	}
	var out []byte
	tab := &span.Table{}
	baseO, baseU := 0, 0
	for j := 0; j < nseg; j++ {
		out = append(out, res[j].out...)
		if bounds[j] < bounds[j+1] {
			tab.Merge(res[j].tab.Shift(baseO, baseU))
		}
		baseO += bounds[j+1] - bounds[j]
		baseU += len(res[j].out)
	}
	return out, tab, nil
}
