// Package api 对外接口：蒙特卡洛积分估计器。依赖 mc。
package api

import (
	"ontology/fn"
	"ontology/mc"
)

// 可判定哨兵错误，四类互不相同。
var (
	ErrInvalidInterval = mc.ErrInvalidInterval // a > b
	ErrNilFunc         = fn.ErrNilFunc         // f 为 nil
	ErrOutOfRange      = mc.ErrOutOfRange      // x < a 或 x > b
	ErrNoSamples       = mc.ErrNoSamples       // n=0 时 Estimate
)

// Estimator 是蒙特卡洛积分估计器，并发安全。
type Estimator struct {
	acc *mc.Accumulator
}

// New 构造估计器；a > b 或 f 为 nil 时整体失败，不留任何状态。
func New(f func(float64) float64, a, b float64) (*Estimator, error) {
	wf, err := fn.New(f)
	if err != nil {
		return nil, err
	}
	acc, err := mc.New(wf, a, b)
	if err != nil {
		return nil, err
	}
	return &Estimator{acc: acc}, nil
}

// Add 喂入采样点 x；越界时返回 ErrOutOfRange 且不改状态。
func (e *Estimator) Add(x float64) error { return e.acc.Add(x) }

// Estimate 返回 (b-a)*sum/n；n=0 时返回 ErrNoSamples。
func (e *Estimator) Estimate() (float64, error) { return e.acc.Estimate() }

// Samples 返回已接受的采样点个数。
func (e *Estimator) Samples() int64 { return e.acc.Samples() }

// SelfCheck 对内置采样点序列核验四条不变量，全部通过返回 nil。
func (e *Estimator) SelfCheck() error { return e.acc.SelfCheck() }
