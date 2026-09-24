// Package ljoin 处理单条变更并产出变更日志，依赖 jstate。
package ljoin

import (
	"errors"

	"ontology/jstate"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyField = errors.New("ljoin: ID 或 Key 为空")
	ErrDupID      = errors.New("ljoin: ID 已存在")
	ErrNoID       = errors.New("ljoin: ID 不存在")
	ErrTooMany    = errors.New("ljoin: 行数超出上限")
)

type Side int

const (
	L Side = iota
	R
)

type Op int

const (
	Ins Op = iota
	Del
)

// Change 是一条上游变更；删除只按 ID 定位，Key 忽略。
type Change struct {
	Side    Side
	Op      Op
	ID, Key string
}

// Out 是一条变更日志：Plus 为 true 表示 +，RID 为空串表示 NULL。
type Out struct {
	Plus     bool
	LID, RID string
}

// Processor 对单条变更产出日志并推进状态。
type Processor struct {
	st      *jstate.State
	maxRows int
	checked int // 最近一条变更检查过的行数（非导出，不进公开接口）
}

func New(st *jstate.State, maxRows int) *Processor {
	return &Processor{st: st, maxRows: maxRows}
}

// ApplyOne 校验并应用一条变更，返回它产出的日志条目；失败时状态不变。
func (p *Processor) ApplyOne(c Change) ([]Out, error) {
	p.checked = 0
	if c.ID == "" || (c.Op == Ins && c.Key == "") {
		return nil, ErrEmptyField
	}
	st := p.st
	if c.Op == Ins {
		if (c.Side == L && st.HasL(c.ID)) || (c.Side == R && st.HasR(c.ID)) {
			return nil, ErrDupID
		}
		if st.Total()+1 > p.maxRows {
			return nil, ErrTooMany
		}
	}
	var outs []Out
	switch {
	case c.Side == L && c.Op == Ins:
		for _, rid := range st.RIDs(c.Key) { // n(k)=0 时循环体不执行
			p.checked++
			outs = append(outs, Out{true, c.ID, rid})
		}
		if len(outs) == 0 {
			outs = append(outs, Out{true, c.ID, ""})
		}
		st.InsertL(jstate.Row{ID: c.ID, Key: c.Key})
	case c.Side == L && c.Op == Del:
		if !st.HasL(c.ID) {
			return nil, ErrNoID
		}
		k := st.LRow(c.ID).Key
		for _, rid := range st.RIDs(k) {
			p.checked++
			outs = append(outs, Out{false, c.ID, rid})
		}
		if len(outs) == 0 {
			outs = append(outs, Out{false, c.ID, ""})
		}
		st.DeleteL(c.ID)
	case c.Side == R && c.Op == Ins:
		n0 := st.N(c.Key) // 插入前的 n(k)，读 n(k) 不算检查行
		for _, lid := range st.LIDs(c.Key) {
			p.checked++
			if n0 == 0 {
				outs = append(outs, Out{false, lid, ""})
			}
			outs = append(outs, Out{true, lid, c.ID})
		}
		st.InsertR(jstate.Row{ID: c.ID, Key: c.Key})
	default: // R 删除
		if !st.HasR(c.ID) {
			return nil, ErrNoID
		}
		k := st.RRow(c.ID).Key
		st.DeleteR(c.ID)
		empty := st.N(k) == 0 // 删除后的 n(k)
		for _, lid := range st.LIDs(k) {
			p.checked++
			outs = append(outs, Out{false, lid, c.ID})
			if empty {
				outs = append(outs, Out{true, lid, ""})
			}
		}
	}
	return outs, nil
}
