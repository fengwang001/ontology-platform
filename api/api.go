// Package api 是变更流保留首条去重的对外接口：整批提交、可判定错误、并发只读。
package api

import "errors"
import "fmt"
import "reflect"
import "sync"
import "ontology/dedup"

var (
	ErrInvalidChange = dedup.ErrInvalidChange // 变更非法（ID 空 / 插入时 Key 空 / Op 非法）
	ErrIDExists      = dedup.ErrIDExists      // 插入的 ID 当前已存活
	ErrIDMissing     = dedup.ErrIDMissing     // 撤回的 ID 当前不存活
	ErrRowLimit      = dedup.ErrRowLimit      // 存活行数超过 maxRows
)

type Change = dedup.Change
type Out = dedup.Entry
type API struct { // API 是去重视图的进程内句柄，并发安全
	mu      sync.RWMutex
	maxRows int
	e       *dedup.Engine
}

func New(maxRows int) *API { return &API{maxRows: maxRows, e: dedup.New()} } // maxRows<=0 不限
func (a *API) Apply(changes []Change) ([]Out, error) { // 任一条被拒则整批不生效
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.e.Commit(changes, a.maxRows)
}
func (a *API) View() map[string]Out { // 下游应用全部日志后的物化视图
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e.View()
}
func ins(id, key string, t int64) Change { return Change{Op: dedup.OpInsert, ID: id, Key: key, T: t} }
func del(id string) Change               { return Change{Op: dedup.OpDelete, ID: id} }
func ent(op byte, key string, r Out) Out { return Out{Op: op, Key: key, ID: r.ID, T: r.T} }
func naive(lives map[string]Out) map[string]Out { // 朴素批量重算：按 Key 取 (T,ID) 最小者
	m := map[string]Out{}
	for _, r := range lives {
		if c, ok := m[r.Key]; ok && (r.T > c.T || r.T == c.T && r.ID > c.ID) {
			continue
		}
		m[r.Key] = r
	}
	return m
}
func checkPrefixes(log []Out) error { // 任一前缀：每 Key 至多一行，- 恰撤当前行
	cur := map[string]Out{}
	for _, e := range log {
		v, ok := cur[e.Key]
		if e.Op == dedup.OpInsert && ok ||
			e.Op == dedup.OpDelete && (!ok || v.ID != e.ID || v.T != e.T) {
			return errors.New("change log prefix not self-consistent")
		}
		if e.Op == dedup.OpInsert {
			cur[e.Key] = e
		} else {
			delete(cur, e.Key)
		}
	}
	return nil
}
func stepWant(key string, before, after map[string]Out) []Out { // 首条变化时应有的日志
	b, bok := before[key]
	n, nok := after[key]
	if bok == nok && b == n {
		return []Out{}
	}
	w := []Out{}
	if bok {
		w = append(w, ent('-', key, b))
	}
	if nok {
		w = append(w, ent('+', key, n))
	}
	return w
}
func (a *API) SelfCheck() error { // 九步序列、负 T、T 并列、四类拒绝不留痕
	seq := []Change{
		ins("a", "k", 5), ins("b", "k", 8), ins("p", "k", 3), ins("m", "k", 3), del("m"),
		ins("e", "k", 1), del("b"), del("e"), del("p"), ins("z", "k", -7), del("z"),
		ins("r1", "q", 2), ins("r2", "q", 2), del("r1"), del("r2")}
	v := New(0)
	lives, log := map[string]Out{}, []Out{}
	for i, c := range seq {
		before := naive(lives)
		key := c.Key // 撤回只按 ID 定位：真实 Key 从存活账本查，输入里的 Key 被忽略
		if c.Op == '-' {
			key = lives[c.ID].Key
		}
		out, err := v.Apply([]Change{c})
		if err != nil || len(out) > 2 {
			return fmt.Errorf("step %d: err=%v entries=%d", i+1, err, len(out))
		}
		if c.Op == '+' {
			lives[c.ID] = Out{Op: '+', Key: c.Key, ID: c.ID, T: c.T}
		} else {
			delete(lives, c.ID)
		}
		after := naive(lives)
		want := stepWant(key, before, after)
		if !reflect.DeepEqual(out, want) {
			return fmt.Errorf("step %d: entries %v, want %v", i+1, out, want)
		}
		log = append(log, out...)
		if checkPrefixes(log) != nil || !reflect.DeepEqual(v.View(), after) {
			return fmt.Errorf("step %d: log/view diverges from batch recompute", i+1)
		}
	}
	return rejectChecks()
}
func rejectChecks() error { // 四类拒绝互异、整批不留痕、then 非空时拒绝后仍可用
	A := []Change{ins("a", "k", 1)}
	cases := []struct {
		seed, batch, then []Change
		max               int
		want              error
	}{
		{nil, []Change{ins("", "k", 1)}, nil, 0, ErrInvalidChange},
		{nil, []Change{ins("a", "", 1)}, nil, 0, ErrInvalidChange},
		{A, []Change{ins("a", "k", 9)}, nil, 0, ErrIDExists},
		{A, []Change{ins("b", "q", 1), ins("a", "k", 9)}, nil, 0, ErrIDExists},
		{nil, []Change{del("ghost")}, nil, 0, ErrIDMissing},
		{A, []Change{del("a"), ins("b", "q", 1), ins("c", "r", 1)},
			[]Change{del("a"), ins("d", "k", 2)}, 1, ErrRowLimit},
	}
	for _, tc := range cases {
		v := New(tc.max)
		_, _ = v.Apply(tc.seed) // seed 合法；nil 为空操作
		snap := v.View()
		_, err := v.Apply(tc.batch)
		changed := !reflect.DeepEqual(snap, v.View())
		if !errors.Is(err, tc.want) || changed {
			return fmt.Errorf("reject %v: err=%v changed=%v", tc.want, err, changed)
		}
		if tc.then != nil {
			if _, err := v.Apply(tc.then); err != nil {
				return fmt.Errorf("unusable after rejection: %w", err)
			}
		}
	}
	return nil
}
