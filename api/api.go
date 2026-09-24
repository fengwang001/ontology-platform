// Package api 是两级 LWW-Map CRDT 的对外入口：Apply/Merge/View 并发安全；SelfCheck 用内置操作序列核验四条不变量（含独立于 lww 的批量重算）。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/lww"
	"ontology/omap"
)

// API 是并发安全的 CRDT 句柄；零值不可用，请用 New。
type API struct {
	mu sync.RWMutex
	o  *omap.Outer
}

// mergeMu 全局串行化 Merge 避免双实例互锁；Apply/View 只取实例锁，无等待环。
var mergeMu sync.Mutex

// New 创建空实例。
func New() *API { return &API{o: omap.New()} }

// Apply 应用一条操作。
func (a *API) Apply(op omap.Op) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.o.Apply(op)
}

// Merge 增量合并另一实例（nil 与自合并均为空操作）。
func (a *API) Merge(other *API) {
	if other == nil || other == a {
		return
	}
	mergeMu.Lock()
	defer mergeMu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.o.Merge(other.o)
}

// View 返回可见视图的深拷贝快照。
func (a *API) View() map[string]map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.o.View()
}

type ent struct {
	ts  int64
	rep string
	v   int64
}

// batch 独立于 lww 重算：每个外层键取最大墓碑、每个内层键取 LWW 胜者
// （ts 大者、平局 rep 大者），仅保留 ts > 墓碑 的条目。
func batch(ops []omap.Op) map[string]map[string]int64 {
	tomb, win := map[string]int64{}, map[string]map[string]ent{}
	for _, q := range ops {
		if q.Kind == "del" {
			if q.TS > tomb[q.O] {
				tomb[q.O] = q.TS
			}
			continue
		}
		if win[q.O] == nil {
			win[q.O] = map[string]ent{}
		}
		c, ok := win[q.O][q.I]
		if !ok || q.TS > c.ts || (q.TS == c.ts && q.Rep > c.rep) {
			win[q.O][q.I] = ent{q.TS, q.Rep, q.V}
		}
	}
	view := map[string]map[string]int64{}
	for o, im := range win {
		vis := map[string]int64{}
		for i, e := range im {
			if e.ts > tomb[o] {
				vis[i] = e.v
			}
		}
		if len(vis) > 0 {
			view[o] = vis
		}
	}
	return view
}

// SelfCheck 对 NOTES.md 第三节的内置八步序列及 LWW、拒绝路径核验四条
// 不变量，全部通过返回 nil，否则返回第一条失败的描述。
func (a *API) SelfCheck() error {
	p := func(o, i string, v, ts int64, r string) omap.Op {
		return omap.Op{Kind: "put", O: o, I: i, V: v, TS: ts, Rep: r}
	}
	d := func(o string, ts int64) omap.Op { return omap.Op{Kind: "del", O: o, TS: ts} }
	bad := ""
	chk := func(ok bool, m string) {
		if !ok && bad == "" {
			bad = m
		}
	}
	// 步1..5：A、B 独立演进，每步后都必须与批量重算一致（不变量1）。
	A, B := New(), New()
	var all, ao, bo []omap.Op
	for _, s := range []struct {
		x   *API
		q   omap.Op
		own *[]omap.Op
		t   string
	}{{A, p("o", "k1", 100, 1, "A"), &ao, "1"}, {A, p("o", "k2", 200, 2, "A"), &ao, "2"},
		{A, d("o", 3), &ao, "3"}, {B, p("o", "k3", 300, 1, "B"), &bo, "4"},
		{B, p("o", "k4", 400, 2, "B"), &bo, "5"}} {
		*s.own = append(*s.own, s.q)
		all = append(all, s.q)
		chk(s.x.Apply(s.q) == nil, "apply "+s.t)
		chk(reflect.DeepEqual(s.x.View(), batch(*s.own)), "batch recompute "+s.t)
	}
	A.Merge(B) // 步6：墓碑 max(3,0)=3，k1..k4 全被覆盖（不变量2）。
	chk(reflect.DeepEqual(A.View(), batch(all)), "merge matches batch")
	chk(len(A.View()["o"]) == 0, "tombstone propagated: o hidden")
	all = append(all, p("o", "k5", 500, 3, "C")) // 步7：ts==墓碑，迟到写整体忽略。
	chk(A.Apply(all[len(all)-1]) == nil && reflect.DeepEqual(A.View(), batch(all)), "step7 covered")
	all = append(all, p("o", "k6", 600, 4, "D")) // 步8：ts>墓碑，可见。
	chk(A.Apply(all[len(all)-1]) == nil && reflect.DeepEqual(A.View(), batch(all)), "step8 visible")
	x, y := New(), New() // 不变量3：ts 取胜、与到达顺序无关、双向合并收敛。
	chk(x.Apply(p("o", "k", 100, 5, "A")) == nil && x.Apply(p("o", "k", 999, 2, "B")) == nil, "lww x")
	chk(y.Apply(p("o", "k", 999, 2, "B")) == nil && y.Apply(p("o", "k", 100, 5, "A")) == nil, "lww y")
	x.Merge(y)
	y.Merge(x)
	chk(x.View()["o"]["k"] == 100 && reflect.DeepEqual(x.View(), y.View()), "lww converges")
	z := New() // 不变量4：三类哨兵错误互不相同，拒绝后无痕迹且仍可用。
	chk(errors.Is(z.Apply(p("", "i", 1, 1, "A")), lww.ErrEmptyOuter), "empty outer")
	chk(errors.Is(z.Apply(p("o", "", 1, 1, "A")), lww.ErrEmptyInner), "empty inner")
	chk(errors.Is(z.Apply(p("o", "i", 1, 0, "A")), lww.ErrNonPositiveTS), "bad ts")
	chk(lww.ErrEmptyOuter != lww.ErrEmptyInner && lww.ErrEmptyInner != lww.ErrNonPositiveTS, "distinct")
	chk(reflect.DeepEqual(z.View(), map[string]map[string]int64{}), "no trace after rejects")
	chk(z.Apply(p("o", "i", 7, 1, "A")) == nil && z.View()["o"]["i"] == 7, "usable after rejects")
	if bad != "" {
		return fmt.Errorf("api SelfCheck: %s", bad)
	}
	return nil
}
