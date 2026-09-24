// Package ljoin 在 jstate 之上处理单条变更、产出增量左外连接的变更
// 日志条目，并做行数上限校验。仅依赖 jstate。
package ljoin

import (
	"errors"

	"ontology/jstate"
)

// Change 是一条上游变更：Side 指定 L/R，Op 指定插入(+)或删除(-)。
// 删除只按 ID 定位，Key 字段被忽略。
type Change struct {
	Side jstate.Side
	Op   byte // '+' 插入，'-' 删除
	ID   string
	Key  string
}

// Out 是一条下游变更日志条目。RID 为空串表示右表为 NULL 的补位行。
type Out struct {
	Op  byte // '+' 或 '-'
	LID string
	RID string // "" 表示 NULL
}

// 四类可判定、互不相同的哨兵错误。
var (
	ErrInvalidChange = errors.New("ljoin: invalid change (empty id/key or bad side/op)")
	ErrDuplicateID   = errors.New("ljoin: id already exists in the same table")
	ErrMissingID     = errors.New("ljoin: id does not exist in the same table")
	ErrTooManyRows   = errors.New("ljoin: total rows of L and R exceed maxRows")
)

// Engine 持有两表状态与行数上限。
type Engine struct {
	t       *jstate.Tables
	maxRows int
	// checked 是最近一次处理单条变更时检查过的行数（L 与 R 合计；
	// 读 n(k) 不计）。非导出，绝不经任何公开接口暴露。
	checked int
}

// New 创建上限为 maxRows 的引擎（0 表示不限制）。
func New(maxRows int) *Engine { return &Engine{t: jstate.New(), maxRows: maxRows} }

// Tables 暴露底层表，供 api 层重算 View 与自检。
func (e *Engine) Tables() *jstate.Tables { return e.t }

// Apply 顺序处理一批变更；任一条被拒则整批不生效，已产出条目一并丢弃。
func (e *Engine) Apply(changes []Change) ([]Out, error) {
	cand := e.t.Clone()
	log := make([]Out, 0)
	for _, ch := range changes {
		outs, err := e.applyOne(cand, ch)
		if err != nil {
			e.checked = 0
			return nil, err
		}
		log = append(log, outs...)
	}
	e.t = cand
	return log, nil
}

func (e *Engine) applyOne(t *jstate.Tables, ch Change) ([]Out, error) {
	e.checked = 0
	bad := ch.ID == "" ||
		(ch.Op == '+' && ch.Key == "") ||
		(ch.Side != jstate.SideL && ch.Side != jstate.SideR) ||
		(ch.Op != '+' && ch.Op != '-')
	if bad {
		return nil, ErrInvalidChange
	}
	switch ch.Op {
	case '+':
		if t.Has(ch.Side, ch.ID) {
			return nil, ErrDuplicateID
		}
		return e.insert(t, ch)
	default: // '-'
		if !t.Has(ch.Side, ch.ID) {
			return nil, ErrMissingID
		}
		return e.remove(t, ch)
	}
}

func (e *Engine) insert(t *jstate.Tables, ch Change) ([]Out, error) {
	k := ch.Key
	var outs []Out
	if ch.Side == jstate.SideL {
		// 插入 L：n(k)=0 发 NULL 补位，否则与全部匹配 R 行配对。
		if t.Count(jstate.SideR, k) == 0 {
			outs = append(outs, Out{Op: '+', LID: ch.ID})
		} else {
			for _, r := range t.SortedIDs(jstate.SideR, k) {
				e.checked++
				outs = append(outs, Out{Op: '+', LID: ch.ID, RID: r})
			}
		}
	} else {
		// 插入 R：仅当插入前 n(k)=0 才撤回每个匹配 L 的 NULL 行。
		beforeZero := t.Count(jstate.SideR, k) == 0
		for _, l := range t.SortedIDs(jstate.SideL, k) {
			e.checked++
			if beforeZero {
				outs = append(outs, Out{Op: '-', LID: l}, Out{Op: '+', LID: l, RID: ch.ID})
			} else {
				outs = append(outs, Out{Op: '+', LID: l, RID: ch.ID})
			}
		}
	}
	t.Add(ch.Side, ch.ID, k)
	if e.maxRows > 0 && t.Total() > e.maxRows {
		return nil, ErrTooManyRows
	}
	return outs, nil
}

func (e *Engine) remove(t *jstate.Tables, ch Change) ([]Out, error) {
	k, _ := t.KeyOf(ch.Side, ch.ID)
	var outs []Out
	if ch.Side == jstate.SideL {
		// 删除 L：n(k)=0 撤 NULL，否则撤掉与全部匹配 R 的配对。
		if t.Count(jstate.SideR, k) == 0 {
			outs = append(outs, Out{Op: '-', LID: ch.ID})
		} else {
			for _, r := range t.SortedIDs(jstate.SideR, k) {
				e.checked++
				outs = append(outs, Out{Op: '-', LID: ch.ID, RID: r})
			}
		}
		t.Remove(jstate.SideL, ch.ID)
		return outs, nil
	}
	// 删除 R：先逐 L 撤配对；仅当删除后 n(k)=0 才紧接着补回 NULL。
	ls := t.SortedIDs(jstate.SideL, k)
	t.Remove(jstate.SideR, ch.ID)
	fillNull := t.Count(jstate.SideR, k) == 0
	for _, l := range ls {
		e.checked++
		outs = append(outs, Out{Op: '-', LID: l, RID: ch.ID})
		if fillNull {
			outs = append(outs, Out{Op: '+', LID: l})
		}
	}
	return outs, nil
}
