// Package cent 提供质心结构与作用于质心切片的纯函数：排序、压缩、分位数。
// 不依赖本工程其他包。
package cent

import (
	"errors"
	"math"
	"sort"
)

// 哨兵错误（api 包会再导出别名，调用方只需 errors.Is 判定）。
var (
	ErrEmpty         = errors.New("cent: quantile of empty digest")
	ErrQuantileRange = errors.New("cent: quantile q out of [0,1]")
)

// Centroid 代表 count 个值坍缩成的质心，均值为 Mean。
type Centroid struct {
	Mean  float64
	Count int
}

// Sort 按 mean 升序排序，mean 相同时 count 小的在前。
func Sort(cs []Centroid) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Mean != cs[j].Mean {
			return cs[i].Mean < cs[j].Mean
		}
		return cs[i].Count < cs[j].Count
	})
}

// Compress 在质心个数 > k 时，反复合并「相邻 count 之和最小」的一对
// （并列取下标最小者），直到个数 ≤ k。返回压缩后的切片。
func Compress(cs []Centroid, k int) []Centroid {
	for len(cs) > k {
		best, bestSum := 0, cs[0].Count+cs[1].Count
		for i := 1; i+1 < len(cs); i++ {
			if s := cs[i].Count + cs[i+1].Count; s < bestSum {
				best, bestSum = i, s
			}
		}
		a, b := cs[best], cs[best+1]
		n := a.Count + b.Count
		cs[best] = Centroid{
			Mean:  (a.Mean*float64(a.Count) + b.Mean*float64(b.Count)) / float64(n),
			Count: n,
		}
		cs = append(cs[:best+1], cs[best+2:]...)
	}
	return cs
}

// Quantile 对按 Sort 有序的质心切片估计分位数。
// r = q*(n-1)（0 起）；落在某质心秩段 [lo,hi] 内返回其 mean；
// 严格落在相邻两质心之间时返回线性插值。
// 返回的 visits 为定位过程中访问过的质心个数（恒 ≤ len(cs)）。
func Quantile(cs []Centroid, q float64) (v float64, visits int, err error) {
	if math.IsNaN(q) || q < 0 || q > 1 {
		return 0, 0, ErrQuantileRange
	}
	n := 0
	for _, c := range cs {
		n += c.Count
	}
	if n == 0 {
		return 0, 0, ErrEmpty
	}
	r := q * float64(n-1)
	lo := 0
	for i, c := range cs {
		hi := lo + c.Count - 1
		if r <= float64(hi) {
			if r >= float64(lo) {
				return c.Mean, i + 1, nil
			}
			// hi_{i-1} = lo-1 < r < lo：与前一质心线性插值
			p := cs[i-1]
			frac := r - float64(lo-1)
			return p.Mean + (c.Mean-p.Mean)*frac, i + 1, nil
		}
		lo = hi + 1
	}
	return cs[len(cs)-1].Mean, len(cs), nil // 不可达：r ≤ n-1 必在末段内
}
