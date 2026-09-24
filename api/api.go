// Package api 是变更流保留首条去重的对外门面，并发安全。
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/dedup"
)

type Change = dedup.Change
type Out = dedup.Out
type Row = dedup.Row

// 四类可判定、互不相同的哨兵错误。
var (
	ErrInvalidChange = dedup.ErrInvalidChange
	ErrIDExists      = dedup.ErrIDExists
	ErrIDMissing     = dedup.ErrIDMissing
	ErrLimit         = dedup.ErrLimit
)

// API 并发安全：Apply 串行写，View 可并发读。
type API struct {
	mu sync.RWMutex
	d  *dedup.Engine
}

func New(maxRows int) *API { return &API{d: dedup.New(maxRows)} }

// Apply 按顺序应用一批变更，返回本批产出的日志；任一条被拒则整批不生效。
func (a *API) Apply(changes []Change) ([]Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.d.Apply(changes)
}

// View 返回下游应用全部日志后的物化视图（副本，可自由修改）。
func (a *API) View() map[string]Row {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.d.View()
}

// SelfCheck 在独立引擎上用内置序列核验四条不变量，不影响接收者状态。
func (a *API) SelfCheck() error {
	e := dedup.New(1000)
	alive, keyOf := map[string]Row{}, map[string]string{} // 朴素模型：ID -> 行 / Key
	down := map[string]Row{}                              // 下游逐条应用日志前缀后的视图
	naive := func() map[string]Row {
		v := map[string]Row{}
		for id, x := range alive { // 朴素批量重算：分组取 (T,ID) 最小
			x.ID = id
			k := keyOf[id]
			if c, ok := v[k]; !ok || x.T < c.T || x.T == c.T && x.ID < c.ID {
				v[k] = x
			}
		}
		return v
	}
	p := func(op byte, id, key string, t int64) Change {
		return Change{Op: op, ID: id, Key: key, T: t}
	}
	seq := []Change{
		p('+', "a", "k", 5), p('+', "b", "k", 8), p('+', "p", "k", 3), p('+', "m", "k", 3),
		p('-', "m", "", 0), p('+', "e", "k", 1), p('-', "b", "", 0), p('-', "e", "", 0),
		p('-', "p", "", 0), p('+', "z", "q", -2), p('+', "y", "q", -2), p('-', "z", "ignored", 99),
	}
	for _, c := range seq {
		key := c.Key
		if c.Op == '-' {
			key = keyOf[c.ID] // 撤回只按 ID 定位
		}
		b := naive()
		before, hadOld := b[key]
		outs, err := e.Apply([]Change{c})
		if err != nil {
			return err
		}
		if c.Op == '+' {
			alive[c.ID], keyOf[c.ID] = Row{ID: c.ID, T: c.T}, c.Key
		} else {
			delete(alive, c.ID)
			delete(keyOf, c.ID)
		}
		after := naive()
		nw, hasNew := after[key]
		changed := (hadOld || hasNew) && !(hadOld && hasNew && before == nw)
		want := []Out{} // 不变量 3：0/1/2 条，首条不变（含无→无）恰为 0
		if changed {
			if hadOld {
				want = append(want, Out{Op: '-', Key: key, ID: before.ID, T: before.T})
			}
			if hasNew {
				want = append(want, Out{Op: '+', Key: key, ID: nw.ID, T: nw.T})
			}
		}
		if len(outs) != len(want) {
			return errors.New("selfcheck: output count not minimal")
		}
		for i := range want {
			if outs[i] != want[i] {
				return errors.New("selfcheck: output content mismatch")
			}
		}
		for _, o := range outs { // 不变量 2：- 恰好撤回当前行，+ 时该 Key 为空
			cur, ok := down[o.Key]
			if o.Op == '-' && (!ok || cur != (Row{ID: o.ID, T: o.T})) {
				return errors.New("selfcheck: log prefix retract mismatch")
			}
			if o.Op == '+' && ok {
				return errors.New("selfcheck: log prefix inserts over live row")
			}
			if o.Op == '-' {
				delete(down, o.Key)
			} else {
				down[o.Key] = Row{ID: o.ID, T: o.T}
			}
		}
		if !reflect.DeepEqual(down, after) || !reflect.DeepEqual(e.View(), after) {
			return errors.New("selfcheck: view diverges from naive recompute")
		}
	}
	// 不变量 4：四类哨兵互不相同，且每次被拒后视图逐字节不变。
	fx := dedup.New(2)
	if _, err := fx.Apply([]Change{p('+', "a", "k", 1)}); err != nil {
		return err
	}
	snap := fx.View()
	cases := []struct {
		cs       []Change
		sentinel error
	}{
		{[]Change{p('+', "", "k", 1)}, ErrInvalidChange},
		{[]Change{p('+', "x", "", 1)}, ErrInvalidChange},
		{[]Change{p('+', "a", "k", 2)}, ErrIDExists},
		{[]Change{p('+', "b", "k", 2), p('+', "c", "q", 3)}, ErrLimit},
		{[]Change{p('-', "z", "", 0)}, ErrIDMissing},
	}
	for _, tc := range cases {
		if _, err := fx.Apply(tc.cs); !errors.Is(err, tc.sentinel) {
			return errors.New("selfcheck: missing or wrong sentinel error")
		}
		if !reflect.DeepEqual(fx.View(), snap) {
			return errors.New("selfcheck: rejected batch left state behind")
		}
	}
	return nil
}
