// Package ws 维护单组数值的 Welford 在线统计量 (n, mean, M2)。
// 不依赖任何其他包。方差为总体方差 M2/n，标准差为 sqrt(M2/n)。
package ws

import "math"

// W 是单组 Welford 状态。零值即为空组，可直接使用。
type W struct {
	n    int64
	mean float64
	m2   float64
}

// Add 按 Welford 增量公式加入一个值 x。
func (w *W) Add(x float64) {
	w.n++
	delta := x - w.mean
	w.mean += delta / float64(w.n)
	w.m2 += delta * (x - w.mean)
}

// Remove 撤回一个值恰为 x 的元素。调用方必须保证组内存在该值。
// n 归 0 时组重置为空。
func (w *W) Remove(x float64) {
	if w.n <= 1 {
		w.n, w.mean, w.m2 = 0, 0, 0
		return
	}
	oldMean := w.mean
	w.n--
	w.mean += (oldMean - x) / float64(w.n)
	w.m2 -= (x - oldMean) * (x - w.mean)
	// 浮点回撤可能留下微小的负 M2，钳到 0。
	if w.m2 < 0 {
		w.m2 = 0
	}
}

// Merge 把另一组 o 合入本组（Chan 并行方差合并公式）。
// 调用方必须保证 o 非空、且与本组不是同一组。
func (w *W) Merge(o W) {
	n := w.n + o.n
	delta := o.mean - w.mean
	w.mean += delta * float64(o.n) / float64(n)
	w.m2 += o.m2 + delta*delta*float64(w.n)*float64(o.n)/float64(n)
	w.n = n
}

// Count 返回元素个数 n。
func (w W) Count() int64 { return w.n }

// Mean 返回均值；空组返回 0。
func (w W) Mean() float64 { return w.mean }

// M2 返回 Σ(xᵢ−mean)²；空组返回 0。
func (w W) M2() float64 { return w.m2 }

// Variance 返回总体方差 M2/n；空组返回 0。
func (w W) Variance() float64 {
	if w.n == 0 {
		return 0
	}
	return w.m2 / float64(w.n)
}

// Std 返回总体标准差 sqrt(M2/n)；空组返回 0。
func (w W) Std() float64 { return math.Sqrt(w.Variance()) }
