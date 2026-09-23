// Package par 把一段大输入按任意字节偏移切成 K 段，交 K 个 goroutine 并行
// 转码后拼接；结果与单线程 stream 完全相同（输出、统计、错误）。
package par

import (
	"sync"

	"ontology/stream"
)

// Result 是并行转码结果。
type Result struct {
	Out   []byte
	Stats stream.Stats
	Err   error
}

// Transcode 把 in 用 cfg 切成 K 段并行转码。cuts 为 K-1 个内部切点
// （0<cuts[i]<len(in) 且递增）；传 nil 则近似等分。K 取 1..8。
func Transcode(in []byte, cfg stream.Config, cuts []int) Result {
	bounds := bounds(len(in), cuts)
	seg := make([][]byte, len(bounds)-1)
	var st []stream.Stats
	var errs []error
	var wg sync.WaitGroup
	var mu sync.Mutex
	for k := 0; k < len(bounds)-1; k++ {
		s, e := bounds[k], bounds[k+1]
		c := cfg
		c.SkipBefore = s
		c.StopAt = e
		if s != 0 {
			c.EmitBOM = false
		}
		// 段首可能落在字符/非法单元中间：向前回看最多 4 字节对齐。
		start := alignStart(in, s, cfg.Dir)
		wg.Add(1)
		go func(k, start, e int, c stream.Config) {
			defer wg.Done()
			tr := stream.New(c)
			_, _ = tr.Write(in[start:e])
			err := tr.Close()
			mu.Lock()
			seg[k] = tr.Output()
			st = append(st, tr.Stats())
			errs = append(errs, err)
			mu.Unlock()
		}(k, start, e, c)
	}
	wg.Wait()
	var out []byte
	var sum stream.Stats
	for _, b := range seg {
		out = append(out, b...)
	}
	for _, x := range st {
		sum.Scalars += x.Scalars
		sum.BadUnits += x.BadUnits
		sum.BadBytes += x.BadBytes
		sum.BOMBytes += x.BOMBytes
		sum.Consumed += x.Consumed
		sum.Checks += x.Checks
	}
	sum.Consumed = int64(len(in))
	return Result{Out: out, Stats: sum, Err: firstErr(errs)}
}

func bounds(n int, cuts []int) []int {
	if len(cuts) == 0 {
		k := 4
		if n < k {
			k = n
		}
		if k < 1 {
			k = 1
		}
		b := []int{0}
		for i := 1; i < k; i++ {
			b = append(b, n*i/k)
		}
		return append(b, n)
	}
	b := append([]int{0}, cuts...)
	return append(b, n)
}

// alignStart 找到切点 s 所属单元的真正首字节偏移：s 位置（若为非续字节）
// 或 s 之前最后一个非续字节；UTF-8 至多回看 3 字节。UTF-16 回看到偶偏移。
func alignStart(in []byte, s, dir int) int {
	if s == 0 {
		return 0
	}
	if dir == stream.U16LEtoU8 || dir == stream.U16BEtoU8 || dir == stream.U16AutoToU8 {
		return s - s%2
	}
	start := s
	if in[s] >= 0x80 && in[s] <= 0xBF {
		for i := 0; i < 3 && start > 0; i++ {
			start--
			if !(in[start] >= 0x80 && in[start] <= 0xBF) {
				return start
			}
		}
		return start
	}
	return s
}

func firstErr(errs []error) error {
	var first error
	for _, e := range errs {
		if e != nil && first == nil {
			first = e
		}
	}
	return first
}
