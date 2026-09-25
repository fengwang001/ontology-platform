// Package par 把输入切成 K 段并行转码后拼接，结果与单线程流式完全一致。
package par

import (
	"sync"

	"ontology/stream"
)

const overlap = 3 // 回看字节上限；任何非法单元最长 4 字节

// Result 是并行转码结果。
type Result struct {
	Out   []byte
	Stats stream.Stats
	Err   error
}

type seg struct {
	out   []byte
	stats stream.Stats
	err   error
}

// Transcode 按 cuts（段起始偏移，首个必须为 0，升序）并行转码。
func Transcode(p []byte, cuts []int, cfg stream.Config) Result {
	k := len(cuts)
	res := make([]seg, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start, end := cuts[i], len(p)
			if i+1 < k {
				end = cuts[i+1]
			}
			res[i] = runSeg(p, start, end, i == k-1, cfg)
		}(i)
	}
	wg.Wait()

	var out []byte
	var sum stream.Stats
	var firstErr error
	for _, s := range res {
		out = append(out, s.out...)
		sum.Valid += s.stats.Valid
		sum.ValidBytes += s.stats.ValidBytes
		sum.Invalid += s.stats.Invalid
		sum.InvalidBytes += s.stats.InvalidBytes
		sum.BOMBytes += s.stats.BOMBytes
		sum.Consumed += s.stats.Consumed
		sum.Checks += s.stats.Checks
		if s.err != nil && firstErr == nil {
			firstErr = s.err
		}
	}
	return Result{Out: out, Stats: sum, Err: firstErr}
}

func runSeg(p []byte, start, end int, last bool, cfg stream.Config) seg {
	c := cfg
	if start == 0 {
		// 段 0：正常解析；非末段 Close 时不结算 EOF 残尾。
		c.NoTail = !last
		tr := stream.New(c)
		var s seg
		if _, err := tr.Write(p[start:end]); err != nil {
			s.err = err
		}
		if err := tr.Close(); s.err == nil && err != nil {
			s.err = err
		}
		s.out = tr.Output()
		s.stats = tr.Stats()
		s.stats.Consumed = int64(end - start)
		return s
	}
	// 后续段：喂入上一段末尾 overlap 字节 + 本段字节；
	// 前 overlap 字节只驱动状态，起始偏移 < overlap 的单元被丢弃。
	c.SkipPrefix, c.SkipN = true, overlap
	tr := stream.New(c)
	var s seg
	from := start - overlap
	if from < 0 {
		from = 0
	}
	ov := start - from
	if _, err := tr.Write(p[from:end]); err != nil {
		s.err = err
	}
	if err := tr.Close(); s.err == nil && err != nil {
		s.err = err
	}
	s.out = tr.Output()
	s.stats = tr.Stats()
	s.stats.Consumed = int64(end - start)
	s.stats.BOMBytes = 0
	s.stats.Checks -= int64(ov) // 回看字节不计入总量
	return s
}

// EvenCuts 返回把 n 字节切成 k 段的边界（含 0）。
func EvenCuts(n, k int) []int {
	if k < 1 {
		k = 1
	}
	cuts := make([]int, 0, k)
	for i := 0; i < k; i++ {
		cuts = append(cuts, n*i/k)
	}
	return cuts
}
