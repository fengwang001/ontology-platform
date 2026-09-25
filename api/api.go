// Package api 是两阶段分布式屏障的对外门面。
package api

import (
	"errors"
	"fmt"

	"ontology/bar"
)

// 哨兵错误再导出，调用方用 errors.Is 判定。
var (
	ErrEmptyID           = bar.ErrEmptyID
	ErrRegistryFull      = bar.ErrRegistryFull
	ErrDuplicateArrive   = bar.ErrDuplicateArrive
	ErrEarlyDepart       = bar.ErrEarlyDepart
	ErrNotArrived        = bar.ErrNotArrived
	ErrArriveAfterDepart = bar.ErrArriveAfterDepart
)

// Barrier 是可复用的两阶段屏障。
type Barrier struct {
	b *bar.Barrier
}

// New 创建容量为 n 的屏障。
func New(n int) *Barrier { return &Barrier{b: bar.New(n)} }

// Arrive 记录 id 本轮到达（首次调用即登记）。
func (a *Barrier) Arrive(id string) error { return a.b.Arrive(id) }

// Depart 记录 id 本轮离开。
func (a *Barrier) Depart(id string) error { return a.b.Depart(id) }

// Round 返回当前轮次（从 0 起）。
func (a *Barrier) Round() int { return a.b.Round() }

// Released 报告本轮是否已释放。
func (a *Barrier) Released() bool { return a.b.Released() }

// SelfCheck 用一组内置进程序列核验四条不变量，全部通过返回 nil。
// 自检在独立的内部屏障上进行，不影响接收者的状态。
func (a *Barrier) SelfCheck() error {
	const n = 3
	ids := []string{"s1", "s2", "s3"}

	// 不变量 1：到齐才释放。
	b1 := bar.New(n)
	for i, id := range ids {
		if err := b1.Arrive(id); err != nil {
			return fmt.Errorf("selfcheck inv1 arrive: %w", err)
		}
		if want := i == n-1; b1.Released() != want {
			return fmt.Errorf("selfcheck inv1: released=%v after %d arrivals, want %v", b1.Released(), i+1, want)
		}
	}

	// 不变量 2：离开才推进。
	for i, id := range ids {
		if err := b1.Depart(id); err != nil {
			return fmt.Errorf("selfcheck inv2 depart: %w", err)
		}
		if want := 0; i < n-1 && b1.Round() != want {
			return fmt.Errorf("selfcheck inv2: round advanced early to %d", b1.Round())
		}
	}
	if b1.Round() != 1 || b1.Released() {
		return errors.New("selfcheck inv2: round did not advance exactly once")
	}

	// 不变量 3：轮次隔离——离开后本轮未结束不得再次 Arrive。
	b3 := bar.New(n)
	for _, id := range ids {
		if err := b3.Arrive(id); err != nil {
			return fmt.Errorf("selfcheck inv3 arrive: %w", err)
		}
	}
	if err := b3.Depart(ids[0]); err != nil {
		return fmt.Errorf("selfcheck inv3 depart: %w", err)
	}
	if err := b3.Arrive(ids[0]); !errors.Is(err, ErrArriveAfterDepart) {
		return fmt.Errorf("selfcheck inv3: got %v, want ErrArriveAfterDepart", err)
	}

	// 不变量 4：失败不留痕——被拒操作不改变状态，屏障仍可正常使用。
	b4 := bar.New(2)
	if err := b4.Arrive("x"); err != nil {
		return fmt.Errorf("selfcheck inv4 arrive: %w", err)
	}
	bad := []error{b4.Arrive(""), b4.Arrive("x"), b4.Depart("x")}
	for _, err := range bad {
		if err == nil {
			return errors.New("selfcheck inv4: invalid op accepted")
		}
	}
	if b4.Released() || b4.Round() != 0 {
		return errors.New("selfcheck inv4: rejected op changed state")
	}
	if err := b4.Arrive("y"); err != nil || !b4.Released() {
		return errors.New("selfcheck inv4: barrier unusable after rejections")
	}
	return nil
}
