// Package api 是净零折叠规范化器的对外接口。依赖 norm（间接依赖 fold）。
package api

import (
	"errors"

	"ontology/norm"
)

// Op 是一条带符号增量：+k 增加 k，-k 撤回 k，k 为正整数。
type Op = norm.Op

// 三类可判定故障，互不相同，可用 errors.Is 判定。
var (
	ErrInvalidDelta  = norm.ErrInvalidDelta
	ErrDepthExceeded = norm.ErrDepthExceeded
	ErrNetOverflow   = norm.ErrNetOverflow
)

// Normalizer 是对外句柄，并发安全。
type Normalizer struct {
	n *norm.Normalizer
}

// New 构造一个 changelog 长度上限为 maxDepth 的规范化器。
func New(maxDepth int) *Normalizer {
	return &Normalizer{n: norm.New(maxDepth)}
}

// Apply 接收一条操作；被拒时状态不变并返回可判定哨兵错误。
func (a *Normalizer) Apply(op Op) error { return a.n.Apply(op) }

// Net 返回净值。
func (a *Normalizer) Net() int64 { return a.n.Net() }

// Changelog 返回未了结序列的副本，正负号即方向。
func (a *Normalizer) Changelog() []int64 { return a.n.Changelog() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 只使用新建的独立实例，不触碰接收者状态，可并发调用。
func (a *Normalizer) SelfCheck() error {
	seqs := [][]Op{
		{5, -5, -8, 8, 7, 2, -7, -2}, // 第三节八步
		{7, -7, 2, -2},               // 同多重集不同顺序
		{3, 3, 3, -3, -3, -3, 1},
		{1, -2, 3, -4, 5, -6, 7},
	}
	for _, ops := range seqs {
		fresh := New(len(ops) + 1)
		var sum int64
		for _, op := range ops {
			if err := fresh.Apply(op); err != nil {
				return err
			}
			sum += op
		}
		log := fresh.Changelog()
		// 不变量 1：与朴素参照（反复相邻抵消到不动点）逐条相同；Net 等于代数和。
		if !equal(log, naiveFold(ops)) || fresh.Net() != sum {
			return errors.New("api: 自检失败：与朴素重放不一致")
		}
		// 不变量 2：无相邻反号等量对。不变量 3：重放 changelog 回到 Net。
		var replay int64
		for i, v := range log {
			if i > 0 && log[i-1]+v == 0 {
				return errors.New("api: 自检失败：存在可再折叠的相邻对")
			}
			replay += v
		}
		if replay != fresh.Net() {
			return errors.New("api: 自检失败：重放 changelog 不等于净值")
		}
	}
	// 不变量 4：被拒操作不留痕。
	fresh := New(1)
	if err := fresh.Apply(9); err != nil {
		return err
	}
	before := fresh.Changelog()
	for _, op := range []Op{0, -0, 5} { // k<=0 非法；深度超限
		if fresh.Apply(op) == nil {
			return errors.New("api: 自检失败：非法操作未被拒绝")
		}
	}
	if fresh.Net() != 9 || !equal(fresh.Changelog(), before) {
		return errors.New("api: 自检失败：被拒操作改变了状态")
	}
	return nil
}

// naiveFold 朴素参照实现：反复扫描删除相邻反号等量对直到不动点。
func naiveFold(ops []Op) []int64 {
	s := make([]int64, len(ops))
	copy(s, ops)
	for changed := true; changed; {
		changed = false
		for i := 0; i+1 < len(s); i++ {
			if s[i]+s[i+1] == 0 {
				s = append(s[:i], s[i+2:]...)
				changed = true
				break
			}
		}
	}
	return s
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
