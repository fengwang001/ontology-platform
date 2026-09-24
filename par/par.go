// Package par 把 UTF-8 输入按任意字节偏移切成 K 段并行转码再拼接。
// 切点可能落在多字节字符或非法单元中间；对齐保证与单线程流式结果完全一致。
package par

import (
	"sync"

	"ontology/stream"
	"ontology/u8"
)

type segResult struct {
	out    []byte
	stats  stream.Stats
	err    error
	errOff int64
}

// Align 把名义切点 b 对齐为真正段边界。
// 若 b 前是某个多字节序列的合法未完成前缀，边界前移到其首字节；
// 回看至多 3 个续字节，checks 返回本次回看检查的字节数。
func Align(data []byte, b int) (boundary, checks int) {
	if b <= 0 || b >= len(data) {
		if b < 0 {
			return 0, 0
		}
		return b, 0
	}
	j, n := b-1, 0
	for j >= 0 && u8.Cont(data[j]) && n < 3 {
		j--
		n++
	}
	if j < 0 || u8.LeadLen(data[j]) == 0 {
		return b, n
	}
	var d u8.Decoder
	for k := j; k < b; k++ {
		d.Step(data[k])
	}
	if d.InProgress() {
		return j, n + 1
	}
	return b, n + 1
}

func evenCuts(n, k int) []int {
	cuts := make([]int, 0, k-1)
	for i := 1; i < k; i++ {
		cuts = append(cuts, n*i/k)
	}
	return cuts
}

// Transcode 用 K 个 goroutine 并行转码（From 必须为 UTF-8）。
func Transcode(data []byte, cfg stream.Config, k int) ([]byte, stream.Stats, error) {
	if k < 1 {
		k = 1
	}
	return TranscodeCuts(data, cfg, evenCuts(len(data), k))
}

// TranscodeCuts 用给定名义切点并行转码；切点可任意、可重复。
func TranscodeCuts(data []byte, cfg stream.Config, nominal []int) ([]byte, stream.Stats, error) {
	bounds := []int{0}
	var lookChecks int
	for _, c := range nominal {
		b, ck := Align(data, c)
		lookChecks += ck
		if b > bounds[len(bounds)-1] {
			bounds = append(bounds, b)
		}
	}
	bounds = append(bounds, len(data))

	res := make([]segResult, len(bounds)-1)
	var wg sync.WaitGroup
	for i := 0; i < len(res); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lo, hi := bounds[i], bounds[i+1]
			c := cfg
			c.Resume = i > 0
			tr := stream.New(c)
			_, err := tr.Write(data[lo:hi])
			if err == nil {
				err = tr.Close()
			}
			r := segResult{out: append([]byte(nil), tr.Output()...), stats: tr.Stats(), err: err}
			if ue, ok := err.(*stream.UnitError); ok {
				r.errOff = ue.Offset + int64(lo)
			}
			res[i] = r
		}(i)
	}
	wg.Wait()

	var out []byte
	var sum stream.Stats
	for _, r := range res {
		if r.err != nil {
			if ue, ok := r.err.(*stream.UnitError); ok {
				return nil, sum, &stream.UnitError{Op: unwrap(ue), Offset: r.errOff, Length: ue.Length}
			}
			return nil, sum, r.err
		}
		out = append(out, r.out...)
		s := r.stats
		sum.Accepted += s.Accepted
		sum.Bad += s.Bad
		sum.AcceptedBytes += s.AcceptedBytes
		sum.BadBytes += s.BadBytes
		sum.BOMBytes += s.BOMBytes
		sum.Checks += s.Checks
	}
	sum.Checks += int64(lookChecks)
	return out, sum, nil
}

func unwrap(e *stream.UnitError) error {
	if e.Op != nil {
		return e.Op
	}
	return stream.ErrIllegal
}
