// Package api 是三维 CUBE 增量维护的对外接口。依赖 cube。
package api

import (
	"errors"
	"fmt"

	"ontology/cube"
	"ontology/dim"
)

// 三类可判定错误，互不相同。
var (
	ErrMaxCells     = errors.New("api: maxCells must be positive")
	ErrCellLimit    = cube.ErrCellLimit
	ErrFactNotFound = cube.ErrFactNotFound
)

// Fact 是一条上游事实：三个维度取值 + 带符号度量。
type Fact struct {
	A, B, C string
	V       int64
}

// Cell 是视图中的一个非空 cell。AllX 为 true 表示该维 ALL；
// 为 false 时对应字段是具体取值（"" 是合法具体值）。
type Cell struct {
	A, B, C          string
	AllA, AllB, AllC bool
	Sum              int64
}

// Cube 是对外句柄，goroutine 安全。
type Cube struct{ c *cube.Cube }

// New 构造实例；maxCells 非正返回 ErrMaxCells。
func New(maxCells int) (*Cube, error) {
	if maxCells <= 0 {
		return nil, ErrMaxCells
	}
	return &Cube{c: cube.New(maxCells)}, nil
}

// Add 累加一条事实；超 maxCells 返回 ErrCellLimit 且不留痕。
func (x *Cube) Add(f Fact) error { return x.c.Add(f.A, f.B, f.C, f.V) }

// Remove 减去一条事实；事实不存在返回 ErrFactNotFound 且不留痕。
func (x *Cube) Remove(f Fact) error { return x.c.Remove(f.A, f.B, f.C, f.V) }

// View 返回所有求和不为 0 的 cell，确定性排序。
func (x *Cube) View() []Cell {
	es := x.c.View()
	out := make([]Cell, len(es))
	for i, e := range es {
		k := e.Key
		out[i] = Cell{
			A: k.V[0], B: k.V[1], C: k.V[2],
			AllA: !k.Conc[0], AllB: !k.Conc[1], AllC: !k.Conc[2],
			Sum: e.Sum,
		}
	}
	return out
}

// Level 返回 cell 键中具体（非 ALL）维度的个数，0..3。
func Level(c Cell) int {
	n := 0
	for _, all := range [3]bool{c.AllA, c.AllB, c.AllC} {
		if !all {
			n++
		}
	}
	return n
}

// SelfCheck 在独立实例上核验四条不变量，全部通过返回 nil。
// 不触碰接收者状态，可并发调用。
func (x *Cube) SelfCheck() error {
	facts := []Fact{{"a", "b", "c", 2}, {"a", "d", "c", 5}, {"e", "b", "c", -3}, {"", "b", "d", 4}}
	// 不变量 1+3：逐条 Add 后与批量重算逐 cell 对拍，并核对 level。
	fresh, err := New(1 << 20)
	if err != nil {
		return err
	}
	batch := map[dim.Key]int64{}
	for _, f := range facts {
		if err := fresh.Add(f); err != nil {
			return fmt.Errorf("selfcheck add: %w", err)
		}
		for _, k := range dim.Cells(f.A, f.B, f.C) {
			batch[k] += f.V
		}
	}
	for _, e := range fresh.c.View() {
		if batch[e.Key] != e.Sum {
			return fmt.Errorf("selfcheck: cell %+v got %d want %d", e.Key, e.Sum, batch[e.Key])
		}
	}
	// 不变量 3：单事实恰好触碰 8 个 cell，level 分布恒为 1/3/3/1。
	one, _ := New(1 << 20)
	if err := one.Add(Fact{"u", "v", "w", 1}); err != nil {
		return err
	}
	var hist [4]int
	for _, c := range one.View() {
		hist[Level(c)]++
	}
	if hist != [4]int{1, 3, 3, 1} {
		return fmt.Errorf("selfcheck: level histogram %v", hist)
	}
	// 不变量 2：任意状态下 Add(f) 再 Remove(f) 精确还原。
	before := fmt.Sprint(fresh.c.View())
	if err := fresh.Add(Fact{"z", "y", "x", 9}); err != nil {
		return err
	}
	if err := fresh.Remove(Fact{"z", "y", "x", 9}); err != nil {
		return err
	}
	if fmt.Sprint(fresh.c.View()) != before {
		return errors.New("selfcheck: add/remove did not restore state")
	}
	// 不变量 4：三类拒绝均不留痕。
	if _, err := New(0); !errors.Is(err, ErrMaxCells) {
		return errors.New("selfcheck: New(0) not rejected")
	}
	small, _ := New(1)
	if err := small.Add(Fact{"p", "q", "r", 1}); !errors.Is(err, ErrCellLimit) {
		return errors.New("selfcheck: cell limit not enforced")
	}
	if err := small.Remove(Fact{"p", "q", "r", 1}); !errors.Is(err, ErrFactNotFound) {
		return errors.New("selfcheck: missing fact not rejected")
	}
	if len(small.View()) != 0 {
		return errors.New("selfcheck: rejected op left trace")
	}
	return nil
}
