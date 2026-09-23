// Package par 把 UTF-8 输入按任意字节偏移切 K 段并行转码，结果与单流完全相同。
package par

import (
	"ontology/stream"
	"ontology/u8"
)

// Result 是并行转码结果。
type Result struct {
	Out    []byte
	Stats  stream.Stats
	Checks int64
	Err    error
}

type seg struct{ start, end int }

// plan 按 k 个等长目标切点做对齐；planChecks 统计对齐检查次数（每字节 ≤1 次）。
func plan(data []byte, k int) (segs []seg, planChecks int64) {
	if k < 1 {
		k = 1
	}
	n := len(data)
	cuts := make([]int, 0, k+1)
	cuts = append(cuts, 0)
	for i := 1; i < k; i++ {
		cuts = append(cuts, n*i/k)
	}
	cuts = append(cuts, n)
	bounds := make([]int, k+1)
	bounds[0], bounds[k] = 0, n
	for i := 1; i < k; i++ {
		c := cuts[i]
		if c <= bounds[i-1] {
			bounds[i] = c // 退化为空段
			continue
		}
		b := c
		if u8.Span(data[c]) != 0 {
			// cut 恰是首字节，边界即 cut
		} else {
			for j := c - 1; j >= 0 && j >= c-4; j-- {
				planChecks++
				if sp := u8.Span(data[j]); sp != 0 {
					if j+sp > c {
						b = j + sp // 跨界序列整体归前段
					}
					break
				}
			}
		}
		if b < bounds[i-1] {
			b = bounds[i-1]
		}
		bounds[i] = b
	}
	for i := 0; i < k; i++ {
		if bounds[i] < bounds[i+1] {
			segs = append(segs, seg{bounds[i], bounds[i+1]})
		}
	}
	return segs, planChecks
}

// Run 并行转码 data（仅 UTF-8 输入）。
func Run(data []byte, k int, cfg stream.Config) Result {
	segs, pc := plan(data, k)
	type piece struct {
		out    []byte
		st     stream.Stats
		checks int64
		err    error
		idx    int
	}
	ch := make(chan piece, len(segs))
	for idx, s := range segs {
		go func(idx int, s seg) {
			c := cfg
			c.MaxCache = 3
			// BOM 仅在整个流开头识别：只有包含字节 0 的段允许识别，
			// 且段必须从 0 开始（首段边界恒为 0）。
			if s.start != 0 {
				c.NoBOM = true
			}
			tr := stream.New(c)
			p := data[s.start:s.end]
			_, err := tr.Write(p)
			if err == nil {
				err = tr.Close()
			} else if ie, ok := err.(*stream.InvalidError); ok {
				ie.Offset += s.start
			}
			ch <- piece{out: tr.Output(), st: tr.Stat(), checks: tr.Checks(), err: err, idx: idx}
		}(idx, s)
	}
	var res Result
	res.Checks = pc
	pieces := make([]piece, len(segs))
	for range segs {
		pr := <-ch
		pieces[pr.idx] = pr
	}
	for _, pr := range pieces {
		if res.Err == nil && pr.err != nil {
			res.Err = pr.err
		}
		res.Out = append(res.Out, pr.out...)
		res.Stats.ValidUnits += pr.st.ValidUnits
		res.Stats.InvalidUnits += pr.st.InvalidUnits
		res.Stats.InvalidBytes += pr.st.InvalidBytes
		res.Stats.BOMBytes += pr.st.BOMBytes
		res.Stats.Consumed += pr.st.Consumed
		res.Checks += pr.checks
	}
	return res
}
