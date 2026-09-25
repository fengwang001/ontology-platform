// Package par 把输入切成 K 段并行转码，切点任意且结果与单线程流式一致。
package par

import "ontology/stream"

// Result 是并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
}

type seg struct {
	data   []byte
	base   int64
	first  bool
	last   bool
}

// Transcode 把 in 切成 K 段（K 1..8），用 K 个 goroutine 并行转码后拼接。
// 切点可为任意字节偏移：内部把每段起点回退到上一段最后一个解析边界。
func Transcode(in []byte, k int, cfg stream.Config) Result {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	bounds := cuts(len(in), k)
	segs := align(in, bounds, cfg.From == stream.UTF8)

	outs := make([][]byte, len(segs))
	st := make([]stream.Stats, len(segs))
	done := make(chan int, len(segs))
	for i, s := range segs {
		go func(i int, s seg) {
			c := cfg
			c.OffsetBase = s.base
			c.MidStream = !s.first
			tr := stream.New(c)
			tr.Fragment = !s.last
			_, _ = tr.Write(s.data)
			_ = tr.Close()
			outs[i] = tr.Output()
			st[i] = tr.Stats()
			done <- i
		}(i, s)
	}
	for range segs {
		<-done
	}

	r := Result{}
	for _, o := range outs {
		r.Output = append(r.Output, o...)
	}
	for _, x := range st {
		addStats(&r.Stats, x)
	}
	return r
}

// cuts 返回 K+1 个单调边界（0..n），仅按字节长度均匀切分。
func cuts(n, k int) []int {
	b := make([]int, k+1)
	for i := 0; i <= k; i++ {
		b[i] = n * i / k
	}
	return b
}

// align 把朴素切点修正为真正的解析边界。
// 回退量对 UTF-8 ≤3 字节、UTF-16 ≤4 字节，且不早于上一段起点。
func align(in []byte, cuts []int, isUTF8 bool) []seg {
	back := 3
	if !isUTF8 {
		back = 4
	}
	starts := make([]int, len(cuts)-1)
	prev := 0
	for j := 0; j < len(starts); j++ {
		s := cuts[j+1] - back
		if s < prev {
			s = prev
		}
		starts[j] = s
		prev = s
	}
	segs := make([]seg, 0, len(starts)+1)
	start := 0
	for j, end := range starts {
		segs = append(segs, seg{in[start:end], int64(start), j == 0, false})
		start = end
	}
	segs = append(segs, seg{in[start:], int64(start), len(starts) == 0, true})
	return segs
}

func addStats(a *stream.Stats, b stream.Stats) {
	a.Scalars += b.Scalars
	a.Illegal += b.Illegal
	a.IllegalBytes += b.IllegalBytes
	a.BOMBytes += b.BOMBytes
	a.Consumed += b.Consumed
	a.Checked += b.Checked
}
