// Package api 是蒙特卡洛积分对外的门面：构造、喂点、估计、自检。
package api

import (
	"errors"

	"ontology/fn"
	"ontology/mc"
)

// 四类可判定哨兵错误，互不相同。
var (
	// ErrInvalidInterval 表示区间非法（a > b）。
	ErrInvalidInterval = errors.New("api: invalid interval")
	// ErrNilFunc 表示被积函数缺失（f 为 nil）。
	ErrNilFunc = fn.ErrNilFunc
	// ErrOutOfRange 表示采样点越界（x < a 或 x > b）。
	ErrOutOfRange = mc.ErrOutOfRange
	// ErrNoSamples 表示 n=0 时查询估计。
	ErrNoSamples = mc.ErrNoSamples
)

// Integrator 是蒙特卡洛积分器，并发安全。
type Integrator struct {
	ac *mc.Accumulator
}

// New 构造积分器。a > b 返回 ErrInvalidInterval，f 为 nil 返回 ErrNilFunc；
// 失败时不产生任何状态。
func New(f func(float64) float64, a, b float64) (*Integrator, error) {
	if a > b {
		return nil, ErrInvalidInterval
	}
	g, err := fn.New(f)
	if err != nil {
		return nil, err
	}
	return &Integrator{ac: mc.New(g, a, b)}, nil
}

// Add 喂入采样点 x；越界返回 ErrOutOfRange 且状态不变。
func (in *Integrator) Add(x float64) error { return in.ac.Add(x) }

// Estimate 返回积分估计 (b-a)*sum/n；n=0 返回 ErrNoSamples。
func (in *Integrator) Estimate() (float64, error) { return in.ac.Estimate() }

// Samples 返回已接受的采样点个数。
func (in *Integrator) Samples() int64 { return in.ac.Samples() }

// SelfCheck 用内置采样点序列核验四条不变量，全部通过返回 nil。
// 只构造独立临时实例，不触碰接收者状态，可并发调用。
func (in *Integrator) SelfCheck() error {
	// 不变量 1：常数函数精确。
	c, _ := New(func(float64) float64 { return 3.25 }, -1, 4)
	for _, x := range []float64{-1, 0, 4, 2.5} {
		if err := c.Add(x); err != nil {
			return err
		}
	}
	if est, _ := c.Estimate(); est != 3.25*5 {
		return errors.New("selfcheck: constant function not exact")
	}
	// 不变量 2：与朴素参照一致。
	f := func(x float64) float64 { return x*x - x }
	xs := []float64{0.5, 1.5, 1.0, 0.0}
	m, _ := New(f, 0, 2)
	var sum float64
	for _, x := range xs {
		if err := m.Add(x); err != nil {
			return err
		}
		sum += float64(f(x)) // 显式转换阻断 FMA 融合，保证与逐点求值一致
	}
	if est, _ := m.Estimate(); est != 2*sum/float64(len(xs)) {
		return errors.New("selfcheck: mismatch with naive replay")
	}
	// 不变量 3：零宽区间估计为 0。
	z, _ := New(f, 1.5, 1.5)
	if err := z.Add(1.5); err != nil {
		return err
	}
	if est, _ := z.Estimate(); est != 0 {
		return errors.New("selfcheck: zero-width interval not zero")
	}
	// 不变量 4：失败不留痕。
	r, _ := New(f, 0, 2)
	if err := r.Add(1.0); err != nil {
		return err
	}
	before := r.Samples()
	if err := r.Add(3.0); !errors.Is(err, ErrOutOfRange) {
		return errors.New("selfcheck: out-of-range not rejected")
	}
	if _, err := New(f, 2, 1); !errors.Is(err, ErrInvalidInterval) {
		return errors.New("selfcheck: invalid interval not rejected")
	}
	if _, err := New(nil, 0, 1); !errors.Is(err, ErrNilFunc) {
		return errors.New("selfcheck: nil func not rejected")
	}
	e, _ := New(f, 0, 1)
	if _, err := e.Estimate(); !errors.Is(err, ErrNoSamples) {
		return errors.New("selfcheck: empty estimate not rejected")
	}
	if r.Samples() != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
