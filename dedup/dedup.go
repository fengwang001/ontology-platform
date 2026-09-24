// Package dedup 按 Key 维护各自的 rank.Set，处理单条上游变更，
// 并在每个 Key 的首条变化时产出去重变更日志。
package dedup

import (
	"errors"

	"ontology/rank"
)

type Row = rank.Row

const (
	OpPlus  = '+' // 插入
	OpMinus = '-' // 撤回
)

// Change 是上游变更；撤回只按 ID 定位，Key/T 字段被忽略。
type Change struct {
	Op      byte
	ID, Key string
	T       int64
}

// Out 是发给下游的变更日志：+(Key,ID,T) 或 -(Key,ID,T)。
type Out struct {
	Op      byte
	Key, ID string
	T       int64
}

// 四类互不相同的哨兵错误，调用方用 errors.Is 判定。
var (
	ErrInvalidChange = errors.New("invalid change: empty ID, empty insert Key, or unknown Op")
	ErrIDExists      = errors.New("insert rejected: ID already alive")
	ErrIDMissing     = errors.New("retract rejected: ID not alive")
	ErrLimit         = errors.New("insert rejected: alive row count would exceed maxRows")
)

type live struct {
	key string
	row Row
}

// record 是批内已生效的一步，供失败时逆序回滚。空集合保留在 sets 中，
// View 会跳过，故回滚只需按 key 取回集合。
type record struct {
	plus bool
	key  string
	row  Row
}

// Engine 是去重视图增量维护器，本身不加锁；并发由 api 层负责。
type Engine struct {
	maxRows int
	sets    map[string]*rank.Set
	alive   map[string]live
	n       int
}

func New(maxRows int) *Engine {
	return &Engine{maxRows: maxRows, sets: map[string]*rank.Set{}, alive: map[string]live{}}
}

// diff 在首条变化时按 -old、+new 追加日志；首条不变则什么都不追加。
func diff(buf []Out, key string, old, nw Row, hasOld, hasNew bool) []Out {
	if hasOld && hasNew && old == nw {
		return buf
	}
	if hasOld {
		buf = append(buf, Out{Op: OpMinus, Key: key, ID: old.ID, T: old.T})
	}
	if hasNew {
		buf = append(buf, Out{Op: OpPlus, Key: key, ID: nw.ID, T: nw.T})
	}
	return buf
}

func (e *Engine) rollback(done []record, cause error) error {
	for i := len(done) - 1; i >= 0; i-- {
		r := done[i]
		s := e.sets[r.key]
		if r.plus {
			s.Delete(r.row)
			delete(e.alive, r.row.ID)
			e.n--
		} else {
			s.Insert(r.row)
			e.alive[r.row.ID] = live{key: r.key, row: r.row}
			e.n++
		}
	}
	return cause
}

// Apply 按顺序处理一批变更；任一条被拒则存活行与输出全部不变。
func (e *Engine) Apply(changes []Change) ([]Out, error) {
	buf, done := make([]Out, 0), make([]record, 0)
	for _, c := range changes {
		if c.ID == "" || c.Op != OpPlus && c.Op != OpMinus || c.Op == OpPlus && c.Key == "" {
			return nil, e.rollback(done, ErrInvalidChange)
		}
		row := Row{ID: c.ID, T: c.T}
		if c.Op == OpPlus {
			if _, dup := e.alive[c.ID]; dup {
				return nil, e.rollback(done, ErrIDExists)
			}
			if e.n >= e.maxRows {
				return nil, e.rollback(done, ErrLimit)
			}
			s := e.sets[c.Key]
			if s == nil {
				s = rank.New()
				e.sets[c.Key] = s
			}
			old, hasOld := s.Min()
			s.Insert(row)
			e.alive[c.ID] = live{key: c.Key, row: row}
			e.n++
			nw, _ := s.Min()
			buf = diff(buf, c.Key, old, nw, hasOld, true)
			done = append(done, record{plus: true, key: c.Key, row: row})
			continue
		}
		info, ok := e.alive[c.ID]
		if !ok {
			return nil, e.rollback(done, ErrIDMissing)
		}
		s := e.sets[info.key]
		old, hasOld := s.Min()
		s.Delete(info.row)
		delete(e.alive, c.ID)
		e.n--
		nw, hasNew := s.Min()
		buf = diff(buf, info.key, old, nw, hasOld, hasNew)
		done = append(done, record{plus: false, key: info.key, row: info.row})
	}
	return buf, nil
}

// View 返回当前物化视图：每 Key 存活行中排序键最小的一行（副本，空集合跳过）。
func (e *Engine) View() map[string]Row {
	v := make(map[string]Row, len(e.sets))
	for key, s := range e.sets {
		if r, ok := s.Min(); ok {
			v[key] = r
		}
	}
	return v
}
