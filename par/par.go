// Package par normalizes one large buffer by splitting it at arbitrary byte
// offsets into K blocks, processing them concurrently, and splicing their
// outputs and offset maps.
package par

import (
	"sync"

	"ontology/norm"
	"ontology/span"
)

// Result is the spliced normalization output and mapping.
type Result struct {
	Out []byte
	Tab span.Table
}

type block struct{ start, end int }

// blocks greedily walks cuts[0..K] (offsets into data) and extends each block
// end to the next line boundary ('\n' after CRLF handling; a final dangling
// '\r' counts as a boundary at EOF). Every block then starts and ends at a
// resolved boundary except possibly the first (stream start) and the last
// (stream end), so none contains unresolved cross-block state.
func blocks(data []byte, k int) []block {
	if k < 1 {
		k = 1
	}
	n := len(data)
	bs := make([]block, 0, k)
	start := 0
	for j := 1; j <= k; j++ {
		cut := n
		if j < k {
			cut = n * j / k
		}
		if j == k {
			bs = append(bs, block{start, n})
			break
		}
		end := cut
		if end < start {
			end = start
		}
		for end < n {
			if data[end] == '\n' {
				end++
				break
			}
			end++
		}
		if end > n {
			end = n
		}
		bs = append(bs, block{start, end})
		start = end
		if start >= n {
			break
		}
	}
	return bs
}

// Run splits data into K blocks and normalizes them concurrently with cfg.
// Only the final block applies the end policy; interior blocks use Keep.
func Run(data []byte, k int, cfg norm.Config) (Result, error) {
	bs := blocks(data, k)
	out := make([][]byte, len(bs))
	tabs := make([]*span.Table, len(bs))
	errs := make([]error, len(bs))
	var wg sync.WaitGroup
	for j, blk := range bs {
		wg.Add(1)
		go func(j int, blk block) {
			defer wg.Done()
			c := cfg
			if j != len(bs)-1 {
				c.End = norm.Keep
			}
			nm := norm.New(c)
			if _, err := nm.Write(data[blk.start:blk.end]); err != nil {
				errs[j] = err
				return
			}
			if err := nm.Close(); err != nil {
				errs[j] = err
				return
			}
			out[j] = nm.Output()
			tabs[j] = nm.Map()
		}(j, blk)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return Result{}, e
		}
	}
	res := Result{}
	outOff := 0
	for j, blk := range bs {
		res.Out = append(res.Out, out[j]...)
		for _, s := range tabs[j].Segments() {
			res.Tab.Append(blk.start+s.OrigStart, outOff+s.OutStart, s.OrigLen, s.OutLen)
		}
		outOff += len(out[j])
	}
	return res, nil
}
