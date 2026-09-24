// Package par 把一段大输入切成 K 段并行转码（仅支持 UTF-8 输入方向）。
// 切点可落在任意字节偏移；段起点对齐到 ASCII/首字节，尾巴交给前一段，
// 保证结果与单线程流式逐字节一致。
package par

import (
	"sync"

	"ontology/stream"
	"ontology/u8"
)

// Result 是并行转码结果。
type Result struct {
	Out   []byte
	Stats stream.Stats
	Err   error
}

type seg struct {
	start, end int
	index      int
	out        []byte
	st         stream.Stats
	err        error
}

// Transcode 用 k 个 goroutine 把 in 按近似等长切分后并行转码。
// k<1 视为 1，k>8 截断为 8。
func Transcode(in []byte, cfg stream.Config, k int) Result {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	cuts := cuts(in, k)
	segs := make([]seg, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		segs[i] = seg{start: cuts[i], end: cuts[i+1], index: i}
		wg.Add(1)
		go func(s *seg) {
			defer wg.Done()
			runSeg(in, s, cfg)
		}(&segs[i])
	}
	wg.Wait()
	return merge(segs)
}

// cuts 返回 k+1 个边界（含 0 与 len(in)）。初始近似等长，随后把每个内部
// 边界（除第一个段外）向前推到首个 ASCII/首字节，保证段可独立重开始。
func cuts(in []byte, k int) []int {
	c := make([]int, k+1)
	c[0], c[k] = 0, len(in)
	for i := 1; i < k; i++ {
		p := len(in) * i / k
		for p < c[i-1] || (p < len(in) && !u8.IsLead(in[p])) {
			p++
		}
		c[i] = p
	}
	for i := 1; i < k; i++ {
		if c[i] > c[k] {
			c[i] = c[k]
		}
	}
	return c
}

func runSeg(in []byte, s *seg, base stream.Config) {
	cfg := base
	cfg.NoLeadingBOM = s.index != 0
	tr := stream.New(cfg)
	n, err := tr.Write(in[s.start:s.end])
	_ = n
	if err == nil {
		err = tr.Close()
	}
	s.out, s.st, s.err = tr.Output(), tr.Stats(), err
}

func merge(segs []seg) Result {
	var res Result
	var total int
	var firstErr *stream.Error
	var errSeg int
	for i := range segs {
		total += len(segs[i].out)
		s := segs[i].st
		res.Stats.Scalars += s.Scalars
		res.Stats.BadUnits += s.BadUnits
		res.Stats.BadBytes += s.BadBytes
		res.Stats.BOMBytes += s.BOMBytes
		res.Stats.Consumed += s.Consumed
		res.Stats.Checks += s.Checks
		if s.MaxHold > res.Stats.MaxHold {
			res.Stats.MaxHold = s.MaxHold
		}
		if e, ok := segs[i].err.(*stream.Error); ok && firstErr == nil {
			firstErr, errSeg = e, i
		}
	}
	res.Out = make([]byte, 0, total)
	for i := range segs {
		res.Out = append(res.Out, segs[i].out...)
	}
	if firstErr != nil {
		e := *firstErr
		e.Offset += int64(segs[errSeg].start)
		res.Err = &e
	}
	return res
}
