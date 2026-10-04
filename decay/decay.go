// Package decay 实现路由抖动惩罚值的整数逐步衰减。
//
// 每过一个衰减步长 Δ，惩罚值按 p = ⌊p·num/den⌋ 逐步取整衰减。
// p>0 时逐步严格递减，衰减到 0 即停，因此任一次结算的乘法步数
// 不超过 Z（Pmax 衰减到 0 的步数，构造时算一次），与间隔步数无关。
package decay

import "errors"

// ErrInvalidParam 表示构造参数非法。
var ErrInvalidParam = errors.New("decay: invalid parameter")

const (
	maxDelta = 1_000_000
	maxDen   = 1000
)

// Decayer 按固定步长与系数执行整数衰减。
type Decayer struct {
	delta    int64
	num      int64
	den      int64
	maxSteps int // Z：从 Pmax 逐步衰减到 0 的步数
	mulSteps int // 最近一次 Decay/StepsBelow 调用的乘法步数
}

// New 校验 1≤Δ≤10^6、1≤num<den≤1000、pmax≥1，并预算 Z。
func New(delta, num, den, pmax int64) (*Decayer, error) {
	if delta < 1 || delta > maxDelta || num < 1 || num >= den || den > maxDen || pmax < 1 {
		return nil, ErrInvalidParam
	}
	d := &Decayer{delta: delta, num: num, den: den}
	for p := pmax; p > 0; p = p * num / den {
		d.maxSteps++
	}
	return d, nil
}

// Delta 返回衰减步长。
func (d *Decayer) Delta() int64 { return d.delta }

// MaxSteps 返回 Z。
func (d *Decayer) MaxSteps() int { return d.maxSteps }

// MulSteps 返回最近一次 Decay 或 StepsBelow 的乘法步数。
func (d *Decayer) MulSteps() int { return d.mulSteps }

// Decay 对 p 施加 k 步衰减；p 到 0 即停，乘法步数不超过 Z。
func (d *Decayer) Decay(p, k int64) int64 {
	d.mulSteps = 0
	for i := int64(0); i < k && p > 0; i++ {
		p = p * d.num / d.den
		d.mulSteps++
	}
	return p
}

// StepsBelow 返回使 p 自当前值起衰减 j 步后严格小于 pr 的最小正整数 j。
func (d *Decayer) StepsBelow(p, pr int64) int64 {
	if pr < 1 {
		pr = 1
	}
	d.mulSteps = 0
	var j int64
	for {
		p = p * d.num / d.den
		d.mulSteps++
		j++
		if p < pr {
			return j
		}
	}
}
