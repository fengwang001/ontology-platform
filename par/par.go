// Package par transcodes a large UTF-8 input in parallel with K goroutines.
package par

import (
	"ontology/scalar"
	"ontology/stream"
	"ontology/u8"
	"sync"
	"sync/atomic"
)

var checks atomic.Int64

// Checks returns the total byte examinations across all Transcode calls.
func Checks() int64 { return checks.Load() }

// align returns the first unit boundary >= b (see DESIGN.md section 3).
func align(src []byte, b int) int {
	if b <= 0 {
		return 0
	}
	if b >= len(src) {
		return len(src)
	}
	pos := max(b-3, 0)
	for pos < b {
		_, n, st := u8.Decode(src[pos:])
		c := n + 1
		if st == scalar.Short {
			n, c = len(src)-pos, len(src)-pos
		}
		checks.Add(int64(c))
		pos += n
	}
	return pos
}

// Transcode splits src into k segments, transcodes them concurrently and
// concatenates. Input must be UTF-8. Output, stats and the first error
// match a single streaming pass exactly.
func Transcode(src []byte, k int, cfg stream.Config) ([]byte, stream.Stats, error) {
	k = max(k, 1)
	starts := make([]int, k+1)
	for i := 1; i < k; i++ {
		starts[i] = align(src, i*len(src)/k)
	}
	starts[k] = len(src)
	return run(src, starts, cfg)
}

func run(src []byte, starts []int, cfg stream.Config) ([]byte, stream.Stats, error) {
	k := len(starts) - 1
	outs := make([][]byte, k)
	sts := make([]stream.Stats, k)
	errs := make([]error, k)
	var wg sync.WaitGroup
	for i := range k {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := cfg
			c.BaseOff = int64(starts[i])
			c.SkipBOM = starts[i] > 0
			t := stream.New(c)
			_, err := t.Write(src[starts[i]:starts[i+1]])
			if err == nil {
				err = t.Close()
			}
			outs[i], sts[i], errs[i] = t.Output(), t.Stats(), err
			checks.Add(t.Checks())
		}()
	}
	wg.Wait()
	var out []byte
	var st stream.Stats
	for i := range k {
		out = append(out, outs[i]...)
		st.Scalars += sts[i].Scalars
		st.GoodBytes += sts[i].GoodBytes
		st.Invalid += sts[i].Invalid
		st.BadBytes += sts[i].BadBytes
		st.BOMBytes += sts[i].BOMBytes
		st.Consumed += sts[i].Consumed
		if errs[i] != nil {
			return out, st, errs[i]
		}
	}
	return out, st, nil
}
