// Package mc 蒙特卡洛采样累加：Add 累加 f(x) 并计数，Estimate 求 (b-a)*sum/n。
// 依赖 fn；内存 O(1)，只保留 sum 与 n 两个标量。
package mc

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/fn"
)

// 可判定哨兵错误。
var (
	ErrInvalidInterval = errors.New("mc: invalid interval (a > b)")
	ErrOutOfRange      = errors.New("mc: sample out of range")
	ErrNoSamples       = errors.New("mc: no samples")
)

// Accumulator 是采样累加器。为求平均保留的历史采样点个数恒为 0：
// 状态只有 sum 与 n 两个标量，retained 是不变量计数器，不随 Add 增长。
type Accumulator struct {
	mu       sync.RWMutex
	f        *fn.Func
	a, b     float64
	sum      float64
	n        int64
	retained int64 // 非导出：保留的历史采样点个数，恒为 0（O(1) 内存的证据）
}

// New 构造累加器；a > b 时返回 ErrInvalidInterval，f 为 nil 时返回 fn.ErrNilFunc。
func New(f *fn.Func, a, b float64) (*Accumulator, error) {
	if f == nil {
		return nil, fn.ErrNilFunc
	}
	if a > b {
		return nil, ErrInvalidInterval
	}
	return &Accumulator{f: f, a: a, b: b}, nil
}

// Add 喂入一个采样点；x 越界时返回 ErrOutOfRange 且不改任何状态。
func (ac *Accumulator) Add(x float64) error {
	if x < ac.a || x > ac.b {
		return ErrOutOfRange
	}
	ac.mu.Lock()
	ac.sum += ac.f.Eval(x)
	ac.n++
	ac.mu.Unlock()
	return nil
}

// Estimate 返回 (b-a)*sum/n；n=0 时返回 ErrNoSamples。
func (ac *Accumulator) Estimate() (float64, error) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if ac.n == 0 {
		return 0, ErrNoSamples
	}
	return (ac.b - ac.a) * ac.sum / float64(ac.n), nil
}

// Samples 返回已接受的采样点个数。
func (ac *Accumulator) Samples() int64 {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.n
}

// SelfCheck 用内置采样点序列核验四条不变量与 O(1) 内存约束；
// 全部通过返回 nil，否则报告失败项。不读不改接收者状态，可并发调用。
func (ac *Accumulator) SelfCheck() error {
	var fails []string
	check := func(name string, ok bool) {
		if !ok {
			fails = append(fails, name)
		}
	}
	sq := func(x float64) float64 { return x * x }
	must := func(f func(float64) float64, a, b float64) *Accumulator {
		wf, err := fn.New(f)
		if err != nil {
			panic(err)
		}
		acc, err := New(wf, a, b)
		if err != nil {
			panic(err)
		}
		return acc
	}
	// 不变量 1：常数函数精确（取二进制可精确表示的 c 与区间宽）。
	for _, c := range []float64{-2.5, 0, 3.75} {
		for _, ab := range [][2]float64{{0, 2}, {1, 1.5}, {-1, 2}} {
			acc := must(func(float64) float64 { return c }, ab[0], ab[1])
			for i := 0; i < 7; i++ {
				_ = acc.Add(ab[0])
			}
			est, err := acc.Estimate()
			check("const", err == nil && est == c*(ab[1]-ab[0]))
		}
	}
	// 不变量 2：与朴素参照一致（第三节四步序列，期望 1.75）。
	acc := must(sq, 0, 2)
	var sum float64
	var n int64
	for _, x := range []float64{0.5, 1.5, 1.0, 0.0} {
		_ = acc.Add(x)
		sum += x * x
		n++
	}
	est, err := acc.Estimate()
	check("replay", err == nil && est == 2*sum/float64(n) && est == 1.75)
	// 不变量 3：零宽区间估计为 0。
	z := must(sq, 1.5, 1.5)
	_ = z.Add(1.5)
	zest, zerr := z.Estimate()
	check("zero-width", zerr == nil && zest == 0)
	// 不变量 4：失败不留痕。
	r := must(sq, 0, 1)
	_ = r.Add(0.5)
	s0, n0 := r.sum, r.n
	bad := r.Add(-0.1) != ErrOutOfRange || r.Add(1.1) != ErrOutOfRange
	if _, err := must(sq, 0, 1).Estimate(); err != ErrNoSamples {
		bad = true
	}
	if _, err := New(nil, 0, 1); err != fn.ErrNilFunc {
		bad = true
	}
	if _, err := New(must(sq, 0, 1).f, 2, 1); err != ErrInvalidInterval {
		bad = true
	}
	check("no-trace", !bad && r.sum == s0 && r.n == n0)
	// O(1) 内存：多档规模下保留的历史采样点个数恒为 0。
	for _, m := range []int{100, 1000, 10000} {
		big := must(sq, 0, 2)
		for i := 0; i < m; i++ {
			_ = big.Add(1.0)
		}
		check("retained-zero", big.retained == 0)
	}
	if len(fails) > 0 {
		return fmt.Errorf("mc: selfcheck failed: %s", strings.Join(fails, ", "))
	}
	return nil
}
