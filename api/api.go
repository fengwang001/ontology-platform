// Package api 是对外接口层。依赖 svc，不反向依赖。
package api

import (
	"errors"
	"fmt"

	"ontology/svc"
)

// Balancer 是平滑加权轮询负载均衡器，可并发使用。
type Balancer struct {
	reg *svc.Registry
}

// New 以给定权重构造均衡器；配置非法时整体失败。
func New(weights []int) (*Balancer, error) {
	r, err := svc.New(weights)
	if err != nil {
		return nil, err
	}
	return &Balancer{reg: r}, nil
}

// Next 按平滑加权轮询选出下一台服务器，返回下标。
func (b *Balancer) Next() int { return b.reg.Next() }

// SetWeight 更新服务器 i 的权重并重置平滑状态；非法时不留痕。
func (b *Balancer) SetWeight(i, w int) error { return b.reg.SetWeight(i, w) }

// Weight 返回服务器 i 的权重；下标越界返回 -1。
func (b *Balancer) Weight(i int) int { return b.reg.Weight(i) }

// SelfCheck 对一组内置配置核验四条不变量：
// 公平性、Σcw==0、确定性、失败不留痕。全部通过返回 nil。
func SelfCheck() error {
	configs := [][]int{{3, 1, 2}, {1, 1, 1, 1}, {2, 5, 1, 3}, {7}}
	for _, w := range configs {
		if err := checkFairAndSum(w); err != nil {
			return err
		}
		if err := checkDeterministic(w); err != nil {
			return err
		}
	}
	return checkNoTrace()
}

func checkFairAndSum(weights []int) error {
	b, err := New(weights)
	if err != nil {
		return fmt.Errorf("selfcheck: New%v: %w", weights, err)
	}
	W := 0
	for _, x := range weights {
		W += x
	}
	cnt := make([]int, len(weights))
	for i := 0; i < W; i++ {
		cnt[b.Next()]++
		if b.reg.SumCW() != 0 {
			return fmt.Errorf("selfcheck: %v 第 %d 步后 Σcw != 0", weights, i+1)
		}
	}
	for i := range weights {
		if cnt[i] != weights[i] {
			return fmt.Errorf("selfcheck: %v 不公平: 服务器 %d 选中 %d 次, 期望 %d", weights, i, cnt[i], weights[i])
		}
	}
	return nil
}

func checkDeterministic(weights []int) error {
	a, err1 := New(weights)
	c, err2 := New(weights)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("selfcheck: New%v: %v %v", weights, err1, err2)
	}
	for i := 0; i < 2*a.reg.Total(); i++ {
		if x, y := a.Next(), c.Next(); x != y {
			return fmt.Errorf("selfcheck: %v 不确定: 第 %d 步 %d != %d", weights, i, x, y)
		}
	}
	return nil
}

func checkNoTrace() error {
	if _, err := New(nil); err == nil {
		return errors.New("selfcheck: 空配置未被拒绝")
	}
	if _, err := New([]int{1, -1}); err == nil {
		return errors.New("selfcheck: 非正权重配置未被拒绝")
	}
	b, err := New([]int{2, 3})
	if err != nil {
		return err
	}
	b.Next()
	w0, sum0 := b.Weight(0), b.reg.SumCW()
	if b.SetWeight(2, 1) == nil || b.SetWeight(0, 0) == nil {
		return errors.New("selfcheck: 非法 SetWeight 未被拒绝")
	}
	if b.Weight(0) != w0 || b.reg.SumCW() != sum0 {
		return errors.New("selfcheck: 拒绝后状态被改变")
	}
	return nil
}
