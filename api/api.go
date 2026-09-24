// Package api 是对外门面：基数估计器的构造、增删、估计与自检。
package api

import (
	"errors"
	"fmt"

	"ontology/est"
	"ontology/hll"
)

// Estimator 是并发安全的基数估计器。
type Estimator struct{ inner *est.Estimator }

// New 创建估计器；p < 1、S < 0 报可判定错误。
func New(p, S int) (*Estimator, error) {
	e, err := est.New(p, S)
	if err != nil {
		return nil, err
	}
	return &Estimator{inner: e}, nil
}

// Add 加入一个键的一次出现；空键报可判定错误。
func (e *Estimator) Add(key string) error { return e.inner.Add(key) }

// Remove 撤回一个键的一次出现；空键或键不存在报可判定错误。
func (e *Estimator) Remove(key string) error { return e.inner.Remove(key) }

// Card 返回当前基数估计。
func (e *Estimator) Card() float64 { return e.inner.Card() }

// SelfCheck 用内置 Add/Remove 序列核验四条不变量，全部通过返回 nil。
func (e *Estimator) SelfCheck() error {
	// 不变量 3：恰在 d==S+1 转稠密一次，之后不回落
	t, _ := est.New(2, 3)
	for _, k := range []string{"a", "b", "c"} {
		t.Add(k)
	}
	if t.Dense() || t.Card() != 3 {
		return errors.New("selfcheck: 阈值前须稀疏且精确")
	}
	t.Add("d")
	if !t.Dense() {
		return errors.New("selfcheck: d==S+1 须转稠密")
	}
	t.Remove("d")
	t.Remove("c")
	if !t.Dense() {
		return errors.New("selfcheck: 稠密不得回落稀疏")
	}

	// 不变量 1+2：稠密 Ranks 与批量重算逐寄存器一致；Add/Remove 可逆
	d, _ := est.New(4, 2)
	live := map[string]bool{}
	ops := []struct {
		add bool
		key string
	}{}
	for i := 0; i < 60; i++ { // 确定性伪随机操作序列
		k := string(rune('a'+(i*7+i/3)%26)) + string(rune('A'+i%5))
		add := i%3 != 0 || !live[k]
		ops = append(ops, struct {
			add bool
			key string
		}{add, k})
	}
	for _, op := range ops {
		if op.add {
			d.Add(op.key)
			live[op.key] = true
		} else {
			d.Remove(op.key)
			delete(live, op.key)
		}
	}
	fresh, _ := hll.New(4)
	for k := range live {
		fresh.Add(k)
	}
	got, want := d.Ranks(), fresh.Ranks()
	for j := range want {
		if got[j] != want[j] {
			return fmt.Errorf("selfcheck: 寄存器 %d 秩 %d != 重算 %d", j, got[j], want[j])
		}
	}
	before := d.Ranks()
	d.Add("probe")
	d.Remove("probe")
	for j, r := range d.Ranks() {
		if r != before[j] {
			return errors.New("selfcheck: Add 后 Remove 未精确还原")
		}
	}

	// 不变量 2：删除最大秩后回落次大秩（e 秩2、a 秩1 同寄存器 1）
	f, _ := est.New(2, 0)
	f.Add("a")
	f.Add("e")
	f.Remove("e")
	if r := f.Ranks(); r[1] != 1 {
		return errors.New("selfcheck: 删最大秩未回落次大秩")
	}

	// 不变量 4：四类失败互不相同且不留痕
	g, _ := est.New(2, 1)
	g.Add("a")
	c0 := g.Card()
	errs := []error{hll.ErrEmptyKey, hll.ErrNotFound}
	if g.Add("") != errs[0] || g.Remove("nope") != errs[1] {
		return errors.New("selfcheck: 错误不可判定")
	}
	if _, err1 := New(0, 1); !errors.Is(err1, hll.ErrBadP) {
		return errors.New("selfcheck: p 非法未拒绝")
	}
	if _, err2 := New(1, -1); !errors.Is(err2, est.ErrBadS) {
		return errors.New("selfcheck: S 非法未拒绝")
	}
	if g.Card() != c0 || g.Dense() {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}
