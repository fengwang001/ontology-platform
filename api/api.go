// Package api 对外门面：New/Apply/View/SelfCheck。依赖 stats，单向。
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/stats"
)

// Engine 是对外实例。
type Engine struct {
	s *stats.Store
}

// New 创建一个空实例。
func New() *Engine { return &Engine{s: stats.New()} }

// Apply 应用一个操作；被拒时返回可判定哨兵错误且不留痕。
func (e *Engine) Apply(op stats.Op) error { return e.s.Apply(op) }

// View 返回 Key 的快照；空组返回全零。
func (e *Engine) View(key string) stats.View { return e.s.View(key) }

// batch 精确重算一组元素的 (n, mean, M2)。
func batch(xs []float64) (int64, float64, float64) {
	if len(xs) == 0 {
		return 0, 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var m2 float64
	for _, x := range xs {
		d := x - mean
		m2 += d * d
	}
	return int64(len(xs)), mean, m2
}

func close(a, b float64) bool {
	if a == b {
		return true
	}
	d := math.Abs(a - b)
	return d <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 可被测试直接调用；只操作内部新建实例，并发安全。
func (e *Engine) SelfCheck() error {
	s := stats.New()
	// 不变量 1+2：Add/Remove 交错后与批量重算一致（含 Remove 重算 M2）。
	var elems []float64
	seq := []stats.Op{
		{Kind: stats.OpAdd, Key: "g", X: 1}, {Kind: stats.OpAdd, Key: "g", X: 2},
		{Kind: stats.OpAdd, Key: "g", X: 3}, {Kind: stats.OpAdd, Key: "g", X: 5},
		{Kind: stats.OpRemove, Key: "g", X: 1}, {Kind: stats.OpAdd, Key: "g", X: 4},
		{Kind: stats.OpRemove, Key: "g", X: 3},
	}
	for _, op := range seq {
		if err := s.Apply(op); err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
		if op.Kind == stats.OpAdd {
			elems = append(elems, op.X)
		} else {
			for i, v := range elems {
				if v == op.X {
					elems = append(elems[:i], elems[i+1:]...)
					break
				}
			}
		}
	}
	n, mean, m2 := batch(elems)
	v := s.View("g")
	if v.N != n || !close(v.Mean, mean) || !close(v.M2, m2) {
		return errors.New("selfcheck: 不变量1/2 与批量重算不一致")
	}
	// 不变量 3：Merge 含交叉项，与合并后重算一致。
	for _, x := range []float64{10, 20} {
		if err := s.Apply(stats.Op{Kind: stats.OpAdd, Key: "g2", X: x}); err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
	}
	if err := s.Apply(stats.Op{Kind: stats.OpMerge, Key: "g", Other: "g2"}); err != nil {
		return fmt.Errorf("selfcheck merge: %w", err)
	}
	n, mean, m2 = batch(append(elems, 10, 20))
	v = s.View("g")
	if v.N != n || !close(v.Mean, mean) || !close(v.M2, m2) {
		return errors.New("selfcheck: 不变量3 Merge 与重算不一致")
	}
	// 不变量 4：三类拒绝互不相同且不留痕。
	before := s.View("g")
	rejects := []struct {
		op  stats.Op
		err error
	}{
		{stats.Op{Kind: stats.OpRemove, Key: "g", X: 999}, stats.ErrValueNotFound},
		{stats.Op{Kind: stats.OpMerge, Key: "g", Other: "nope"}, stats.ErrEmptyGroup},
		{stats.Op{Kind: stats.OpAdd, Key: "", X: 1}, stats.ErrEmptyKey},
	}
	for _, r := range rejects {
		if err := s.Apply(r.op); !errors.Is(err, r.err) {
			return fmt.Errorf("selfcheck: 拒绝错误不可判定: %v", err)
		}
	}
	if after := s.View("g"); after != before {
		return errors.New("selfcheck: 不变量4 被拒操作改变了状态")
	}
	return nil
}
