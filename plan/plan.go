// Package plan 基于 diff 的比较结果生成修复计划并原子应用。
package plan

import (
	"errors"

	"ontology/diff"
	"ontology/norm"
)

// 操作种类。
const (
	OpInsert = 0
	OpUpdate = 1
	OpDelete = 2
)

// 角色。
const (
	RoleFixer = 1
	RoleAdmin = 2
)

var (
	// ErrInvalid 表示参数非法（含计划为空指针）。
	ErrInvalid = errors.New("plan: invalid argument")
	// ErrPermission 表示权限不足。
	ErrPermission = errors.New("plan: permission denied")
)

// Op 是计划中的一项。
type Op struct {
	ID      int64
	Kind    int
	D       *int64
	C       []byte
	Mask    uint8 // update 时有效，按 diff.ColD/diff.ColC
	Version int64 // insert 恒为 0
}

// Plan 是 Plan 时刻的不可变修复计划。
type Plan struct {
	ops []Op
}

// Ops 返回计划项的不可变视图（深拷贝切片）。
func (p *Plan) Ops() []Op {
	out := make([]Op, len(p.ops))
	for i, op := range p.ops {
		if op.D != nil {
			v := *op.D
			op.D = &v
		}
		if op.C != nil {
			cp := make([]byte, len(op.C))
			copy(cp, op.C)
			op.C = cp
		}
		out[i] = op
	}
	return out
}

// Len 返回计划项数。
func (p *Plan) Len() int { return len(p.ops) }

// HasDelete 报告计划是否含 Delete。
func (p *Plan) HasDelete() bool {
	for _, op := range p.ops {
		if op.Kind == OpDelete {
			return true
		}
	}
	return false
}

// ErrStale 是版本失配错误，携带最小失配 id。
type ErrStale struct{ ID int64 }

func (e *ErrStale) Error() string { return "plan: stale version" }

// Planner 是修复计划器。
type Planner struct {
	t   *diff.T
	cfg norm.Cfg
}

// New 创建计划器。
func New(t *diff.T, cfg norm.Cfg) *Planner { return &Planner{t: t, cfg: cfg} }

// Compare 归并 [lo,hi) 并返回比较结果。
func (p *Planner) Compare(lo, hi int64) ([]diff.ResultRow, error) {
	return p.t.Compare(lo, hi)
}

// Make 生成 [lo,hi) 的修复计划，del 为真时 Extra 生成 Delete。
func (p *Planner) Make(lo, hi int64, del bool) (*Plan, error) {
	rows, src, _, ver, err := p.t.CompareSnapshot(lo, hi)
	if err != nil {
		return nil, ErrInvalid
	}
	pl := &Plan{ops: make([]Op, 0, len(rows))}
	for _, rr := range rows {
		switch rr.Class {
		case diff.ClsMissing:
			s := src[rr.ID]
			pl.ops = append(pl.ops, Op{
				ID:      rr.ID,
				Kind:    OpInsert,
				D:       clonePtr(s.D),
				C:       cloneBytes(s.C),
				Version: 0,
			})
		case diff.ClsChanged:
			s := src[rr.ID]
			op := Op{ID: rr.ID, Kind: OpUpdate, Version: ver[rr.ID]}
			if rr.Changed&diff.ColD != 0 {
				op.Mask |= diff.ColD
				op.D = clonePtr(s.D)
			}
			if rr.Changed&diff.ColC != 0 {
				op.Mask |= diff.ColC
				op.C = cloneBytes(s.C)
			}
			pl.ops = append(pl.ops, op)
		case diff.ClsExtra:
			if del {
				pl.ops = append(pl.ops, Op{
					ID:      rr.ID,
					Kind:    OpDelete,
					Version: ver[rr.ID],
				})
			}
		}
	}
	return pl, nil
}

// Apply 校验权限与版本后整体应用计划，返回 (插入,更新,删除) 数。
func (p *Planner) Apply(role int, pl *Plan) (ni, nu, nd int, err error) {
	if pl == nil {
		return 0, 0, 0, ErrInvalid
	}
	if role != RoleFixer && role != RoleAdmin {
		return 0, 0, 0, ErrPermission
	}
	if pl.HasDelete() && role != RoleAdmin {
		return 0, 0, 0, ErrPermission
	}
	if len(pl.ops) == 0 {
		return 0, 0, 0, nil
	}
	changes := make([]diff.Change, len(pl.ops))
	for i, op := range pl.ops {
		changes[i] = diff.Change{
			ID:      op.ID,
			Kind:    op.Kind,
			D:       clonePtr(op.D),
			C:       cloneBytes(op.C),
			Mask:    op.Mask,
			Version: op.Version,
		}
		switch op.Kind {
		case OpInsert:
			ni++
		case OpUpdate:
			nu++
		case OpDelete:
			nd++
		}
	}
	if err := p.t.Commit(changes); err != nil {
		if se, ok := err.(*diff.StaleError); ok {
			return 0, 0, 0, &ErrStale{ID: se.ID}
		}
		return 0, 0, 0, err
	}
	return ni, nu, nd, nil
}

func clonePtr(q *int64) *int64 {
	if q == nil {
		return nil
	}
	v := *q
	return &v
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
