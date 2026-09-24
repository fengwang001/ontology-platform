// Package api 是撤回流上增量 Top-N 物化视图的对外接口。依赖 topn。
package api

import (
	"fmt"
	"sync"

	"ontology/rank"
	"ontology/topn"
)

type (
	Op     = rank.Change // 一条输入：Add=true 为 +，否则为 −
	Row    = rank.Row
	Change = rank.Change // 一条变更日志：Add=true 进入前 N，否则离开前 N
)

// 四类可判定哨兵错误（互不相同）。
var (
	ErrInvalidParam = topn.ErrInvalidParam
	ErrKeyExists    = topn.ErrKeyExists
	ErrRowMissing   = topn.ErrRowMissing
	ErrOverLimit    = topn.ErrOverLimit
)

// View 是并发安全的增量 Top-N 物化视图。
type View struct {
	mu sync.RWMutex
	v  *topn.View
}

func New(n, maxRows int) (*View, error) {
	t, err := topn.New(n, maxRows)
	if err != nil {
		return nil, err
	}
	return &View{v: t}, nil
}
func (v *View) Apply(ops []Op) ([]Change, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.v.Apply(ops)
}
func (v *View) View() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.v.View()
}
func (v *View) Live() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.v.Live()
}

// replay 核验变更日志每个前缀：− 恰为持有行、+ 未持有、持有数 ≤N；返回终态持有。
func replay(ch []Change, n int) (map[string]Row, error) {
	h := map[string]Row{}
	for i, c := range ch {
		cur, ok := h[c.Row.Key]
		if c.Add {
			if ok {
				return nil, fmt.Errorf("prefix %d: + held %q", i, c.Row.Key)
			}
			h[c.Row.Key] = c.Row
		} else if !ok || cur != c.Row {
			return nil, fmt.Errorf("prefix %d: − not held", i)
		} else {
			delete(h, c.Row.Key)
		}
		if len(h) > n {
			return nil, fmt.Errorf("prefix %d: holds %d>N", i, len(h))
		}
	}
	return h, nil
}

// checkOne 核验 I1（View 与整体排序取前 N 逐行相同，all 已严格排序故同时保证 I3）
// 与 I2（每个前缀自洽，终态等于 View）。
func checkOne(t *topn.View) error {
	got, all := t.View(), t.Ordered().Snapshot()
	k := min(t.N(), len(all))
	if len(got) != k {
		return fmt.Errorf("I1: %d rows want %d", len(got), k)
	}
	for i := 0; i < k; i++ {
		if got[i] != all[i] {
			return fmt.Errorf("I1/I3: row %d %v want %v", i, got[i], all[i])
		}
	}
	h, err := replay(t.Changelog(), t.N())
	if err != nil {
		return fmt.Errorf("I2: %w", err)
	}
	if len(h) != len(got) {
		return fmt.Errorf("I2: holds %d view %d", len(h), len(got))
	}
	for _, r := range got {
		if x, ok := h[r.Key]; !ok || x != r {
			return fmt.Errorf("I2: final diverges at %q", r.Key)
		}
	}
	return nil
}

// tenSteps 是第三节规定的内置十条输入（N=3, maxRows=16）。
func tenSteps() []Op {
	a := func(k string, s int64, add bool) Op { return Op{Row: Row{Key: k, Score: s}, Add: add} }
	return []Op{
		a("a", 50, true), a("b", 70, true), a("c", 50, true), a("d", 60, true), a("e", 50, true),
		a("b", 70, false), a("e", 50, false), a("f", 55, true), a("d", 60, false), a("g", 45, true),
	}
}

// SelfCheck 对内置输入序列核验四条不变量（含 I4 失败不留痕），失败返回错误。
func (v *View) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if err := checkOne(v.v); err != nil {
		return err
	}
	t, _ := topn.New(3, 16)
	for i, op := range tenSteps() {
		if _, err := t.Apply([]Op{op}); err != nil { // 每条之后 I1/I2/I3 均须成立
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if err := checkOne(t); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	want := []Row{{Key: "f", Score: 55}, {Key: "a", Score: 50}, {Key: "c", Score: 50}}
	if got := t.View(); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		return fmt.Errorf("final %v want %v", got, want)
	}
	live, view := t.Live(), fmt.Sprint(t.View()) // I4：单条拒绝不留痕
	bad := []Op{{Row: Row{Key: "a", Score: 1}, Add: true}, {Row: Row{Key: "z", Score: 1}, Add: false}, {Row: Row{Key: "a", Score: 9}, Add: false}}
	for _, b := range bad {
		if _, e := t.Apply([]Op{b}); e == nil || t.Live() != live || fmt.Sprint(t.View()) != view {
			return fmt.Errorf("I4: %+v accepted or changed state", b)
		}
	}
	full, _ := topn.New(2, 2) // I4 超限：填满后再 + 拒绝且不留痕
	if _, e := full.Apply([]Op{{Row: Row{Key: "x", Score: 1}, Add: true}, {Row: Row{Key: "y", Score: 2}, Add: true}}); e != nil {
		return e
	}
	if _, e := full.Apply([]Op{{Row: Row{Key: "z", Score: 3}, Add: true}}); e != ErrOverLimit || full.Live() != 2 {
		return fmt.Errorf("I4: over-limit e=%v live=%d", e, full.Live())
	}
	return nil
}
