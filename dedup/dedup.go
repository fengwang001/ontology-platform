// Package dedup 按 Key 管理各自的有序存活行集合，处理单条变更并产出变更日志。
package dedup

import "errors"
import "ontology/rank"

// 四类互不相同的哨兵错误，调用方用 errors.Is 判定。
var ErrInvalidChange = errors.New("invalid change: empty ID, empty Key on insert, or bad Op")
var ErrIDExists = errors.New("insert rejected: ID already alive")
var ErrIDMissing = errors.New("delete rejected: ID not alive")
var ErrRowLimit = errors.New("insert rejected: live row count exceeds maxRows")

const (
	OpInsert = '+'
	OpDelete = '-'
)

type Change struct {
	Op  byte
	ID  string
	Key string
	T   int64
}

type Entry struct {
	Op  byte
	Key string
	ID  string
	T   int64
}

type loc struct {
	key string
	t   int64
}

// Engine 持有全部 Key 的物化状态。
type Engine struct {
	groups map[string]*group
	byID   map[string]loc // 存活 ID -> 所在 Key 与 T
	nrows  int
}

type group struct {
	set   *rank.Set
	first rank.Row // 当前物化的首条；has 为 true 时有效
	has   bool
}

func New() *Engine {
	return &Engine{groups: map[string]*group{}, byID: map[string]loc{}}
}
func (e *Engine) Rows() int            { return e.nrows }
func (e *Engine) Alive(id string) bool { _, ok := e.byID[id]; return ok }
func valid(c Change) bool {
	return c.ID != "" && (c.Op == OpInsert && c.Key != "" || c.Op == OpDelete)
}

// View 返回逐 Key 的当前首条，表现为该 Key 当前应存活的 + 条目（空 Key 不出现）。
func (e *Engine) View() map[string]Entry {
	m := make(map[string]Entry, len(e.groups))
	for k, g := range e.groups {
		if g.has {
			m[k] = Entry{Op: OpInsert, Key: k, ID: g.first.ID, T: g.first.T}
		}
	}
	return m
}

// emit 计算新首条与旧首条的差异：先撤旧、后增新；首条不变返回 nil。
func (g *group) emit(key string) []Entry {
	nw, ok := g.set.Min()
	if g.has && ok && g.first == nw {
		return nil
	}
	var out []Entry
	if g.has {
		out = append(out, Entry{Op: OpDelete, Key: key, ID: g.first.ID, T: g.first.T})
	}
	if ok {
		out = append(out, Entry{Op: OpInsert, Key: key, ID: nw.ID, T: nw.T})
	}
	g.first, g.has = nw, ok
	return out
}

// ApplyOne 处理一条已通过校验的变更，返回本步产出的 0/1/2 条日志。
func (e *Engine) ApplyOne(c Change) []Entry {
	if c.Op == OpInsert {
		g := e.groups[c.Key]
		if g == nil {
			g = &group{set: rank.New()}
			e.groups[c.Key] = g
		}
		e.byID[c.ID] = loc{key: c.Key, t: c.T}
		e.nrows++
		g.set.Insert(rank.Row{ID: c.ID, T: c.T})
		return g.emit(c.Key)
	}
	l := e.byID[c.ID]
	g := e.groups[l.key]
	delete(e.byID, c.ID)
	e.nrows--
	g.set.Delete(c.ID)
	return g.emit(l.key)
}

// Commit 先整批模拟校验、全部通过后才提交；任一条被拒则存活行与日志全部不变。
func (e *Engine) Commit(cs []Change, maxRows int) ([]Entry, error) {
	tent := map[string]bool{} // 批内对存活状态的临时覆盖
	alive := func(id string) bool {
		if v, ok := tent[id]; ok {
			return v
		}
		return e.Alive(id)
	}
	n := e.nrows
	for _, c := range cs {
		switch {
		case !valid(c):
			return nil, ErrInvalidChange
		case c.Op == OpInsert && alive(c.ID):
			return nil, ErrIDExists
		case c.Op == OpDelete && !alive(c.ID):
			return nil, ErrIDMissing
		case c.Op == OpInsert:
			n++
			if maxRows > 0 && n > maxRows {
				return nil, ErrRowLimit
			}
			tent[c.ID] = true
		default:
			n--
			tent[c.ID] = false
		}
	}
	out := make([]Entry, 0, len(cs))
	for _, c := range cs {
		out = append(out, e.ApplyOne(c)...)
	}
	return out, nil
}
