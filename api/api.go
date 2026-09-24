// Package api 对外提供条件 upsert 表的并发安全接口。依赖 store。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/store"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrBadParam = errors.New("api: R < 1 or maxKeys < 1")
	ErrBadEvent = store.ErrBadEvent
	ErrTooMany  = store.ErrTooMany
)

type Event = store.Event
type Row = store.Row

// Table 是并发安全的条件 upsert 表。
type Table struct {
	mu sync.RWMutex
	st *store.Store
}

func New(R int64, maxKeys int) (*Table, error) {
	if R < 1 || maxKeys < 1 {
		return nil, ErrBadParam
	}
	return &Table{st: store.New(R, maxKeys)}, nil
}

// Apply 整批原子生效：任一条非法或超限，整批不生效。
func (t *Table) Apply(evs []Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.st.Clone()
	for _, e := range evs {
		if err := c.ApplyOne(e); err != nil {
			return err
		}
	}
	t.st = c
	return nil
}
func (t *Table) Get(key string) (Row, bool) { t.mu.RLock(); defer t.mu.RUnlock(); return t.st.Get(key) }

// GetMany 在同一时刻一致地读取多个键的存活行。
func (t *Table) GetMany(keys []string) map[string]Row {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Row, len(keys))
	for _, k := range keys {
		if r, ok := t.st.Get(k); ok {
			out[k] = r
		}
	}
	return out
}
func (t *Table) Tomb(key string) (int64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.st.Tomb(key)
}

// Stats 返回忽略数与全局水位 G。
func (t *Table) Stats() (ignored, g int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.st.Ignored, t.st.G
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
func (t *Table) SelfCheck() error {
	u := func(k string, v int64, s string) Event { return Event{Op: 'U', Key: k, Ver: v, Val: s} }
	d := func(k string, v int64) Event { return Event{Op: 'D', Key: k, Ver: v} }
	// 不变量 1：版本只进不退（R 极大，无清除干扰）。
	t1, _ := New(1<<60, 100)
	exp := int64(0)
	for _, e := range []Event{u("a", 1, "x"), u("a", 3, "y"), u("a", 2, "z"), d("a", 5), u("a", 4, "w")} {
		_ = t1.Apply([]Event{e})
		if e.Ver > exp {
			exp = e.Ver
		}
		cur := int64(0)
		if r, ok := t1.Get("a"); ok {
			cur = r.Ver
		} else if v, ok := t1.Tomb("a"); ok {
			cur = v
		}
		if cur != exp {
			return errors.New("inv1: version regressed or ignored event applied")
		}
	}
	// 不变量 2：满足 Ver > G_before − R 的序列与朴素参照（墓碑永不清除）逐项一致。
	t2, _ := New(10, 10000)
	nrows, ntombs := map[string]Row{}, map[string]int64{}
	g, seed := int64(0), uint64(1)
	for i := 0; i < 500; i++ {
		seed = seed*6364136223846793005 + 1
		e := Event{Op: 'U', Key: fmt.Sprintf("k%d", (seed>>40)%20), Ver: g + 1 + int64((seed>>33)%4), Val: "v"}
		if seed&1 == 0 {
			e.Op = 'D'
		}
		_ = t2.Apply([]Event{e})
		cur := ntombs[e.Key]
		if r, ok := nrows[e.Key]; ok {
			cur = r.Ver
		}
		if e.Ver > cur && e.Op == 'U' {
			nrows[e.Key] = Row{Val: e.Val, Ver: e.Ver}
			delete(ntombs, e.Key)
		} else if e.Ver > cur {
			delete(nrows, e.Key)
			ntombs[e.Key] = e.Ver
		}
		g = e.Ver
	}
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("k%d", i)
		if got, ok := t2.Get(k); (nrows[k] == Row{}) == ok || got != nrows[k] {
			return fmt.Errorf("inv2: key %s mismatch", k)
		}
	}
	// 不变量 3：删除不被旧写入复活。
	t3, _ := New(1<<60, 100)
	_ = t3.Apply([]Event{u("k", 5, "a"), d("k", 10), u("k", 9, "b"), u("k", 10, "c")})
	_, hasRow := t3.Get("k")
	tv, hasTomb := t3.Tomb("k")
	if hasRow || !hasTomb || tv != 10 {
		return errors.New("inv3: old write resurrected or tomb wrong")
	}
	// 不变量 4：失败不留痕。
	t4, _ := New(1<<60, 2)
	_ = t4.Apply([]Event{u("x", 1, "a")})
	ig0, g0 := t4.Stats()
	for i, b := range [][]Event{{{Op: 'U', Ver: 2, Val: "a"}}, {u("y", 2, "b"), u("z", 3, "c")}} {
		if err := t4.Apply(b); err == nil {
			return fmt.Errorf("inv4: case %d accepted", i)
		}
	}
	ig1, g1 := t4.Stats()
	_, perr := New(0, 1)
	if r, ok := t4.Get("x"); ig1 != ig0 || g1 != g0 || !ok || r != (Row{Val: "a", Ver: 1}) || !errors.Is(perr, ErrBadParam) {
		return errors.New("inv4: rejected op left traces or bad param accepted")
	}
	return nil
}
