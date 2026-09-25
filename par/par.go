// Package par 把一段 UTF-8 输入切成 K 段并行转码；结果与单线程流式完全一致。
// 仅支持 UTF-8 输入（依赖方向：par → stream、u8）。
package par

import (
	"sync"

	"ontology/stream"
	"ontology/u8"
)

type seg struct {
	start, end int
	out        []byte
	stats      stream.Stats
	checks     int
}

// alignStart 把候选切点 b 向左对齐到一个「可立刻定案」的边界。
func alignStart(in []byte, b int) int {
	if b <= 0 {
		return 0
	}
	s := b
	for s > 0 && u8.IsCont(in[s-1]) {
		s--
	}
	if s > 0 {
		l := u8.LeadLen(in[s-1])
		if l > 0 && b-s < l-1 {
			s-- // 首字节的前缀未在左段终止，整序列归右段
		}
	}
	return s
}

// bounds 返回 K 个互不重叠且覆盖全输入的段边界。
func bounds(in []byte, k int) []int {
	if k > len(in) {
		k = len(in)
	}
	if k < 1 {
		k = 1
	}
	b := make([]int, k+1)
	b[0], b[k] = 0, len(in)
	for i := 1; i < k; i++ {
		raw := len(in) * i / k
		b[i] = alignStart(in, raw)
	}
	// 对齐后可能产生空段或逆序，做一次稳定化：保证单调不减。
	for i := 1; i < k; i++ {
		if b[i] < b[i-1] {
			b[i] = b[i-1]
		}
	}
	return b
}

// Result 是并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
	Checks int
}

func runSegment(in []byte, cfg stream.Config, start, end int, last bool) seg {
	cfg.BaseOff = start
	cfg.NoInputBOM = start != 0
	cfg.NoEOF = !last
	tr := stream.New(cfg)
	if _, err := tr.Write(in[start:end]); err != nil {
		panic(err) // 并行路径仅用于替换模式；调用方保证合法 cfg
	}
	if err := tr.Close(); err != nil {
		panic(err)
	}
	return seg{start: start, end: end, out: tr.Output(), stats: tr.Stats(), checks: tr.Checks()}
}

// Transcode 用最多 k 个 goroutine 并行转码 in；cfg.Strict 必须为 false。
func Transcode(in []byte, cfg stream.Config, k int) Result {
	cfg.Strict = false
	bnd := bounds(in, k)
	segs := make([]seg, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		if bnd[i] == bnd[i+1] && !(i == k-1) {
			segs[i] = seg{start: bnd[i], end: bnd[i+1]}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			segs[i] = runSegment(in, cfg, bnd[i], bnd[i+1], i == k-1)
		}(i)
	}
	wg.Wait()
	var res Result
	for _, s := range segs {
		res.Output = append(res.Output, s.out...)
		res.Stats.Scalars += s.stats.Scalars
		res.Stats.Invalid += s.stats.Invalid
		res.Stats.BadBytes += s.stats.BadBytes
		res.Stats.BOMBytes += s.stats.BOMBytes
		res.Stats.Consumed += s.stats.Consumed
		res.Stats.LegalBytes += s.stats.LegalBytes
		res.Checks += s.checks
	}
	return res
}
