// Package plan 依据 diff.Compare 的结果生成修复计划，并提供角色受控的原子应用。
package plan

import (
	"errors"
	"fmt"

	"ontology/diff"
)

// 角色常量。
const (
	RoleRepairer = 1 // 修复员：不可应用含 Delete 的计划
	RoleAdmin    = 2 // 管理员：可应用任意计划
)

// 计划项类型。
const (
	KindInsert = 0
	KindUpdate = 1
	KindDelete = 2
)

// ErrParam 为参数非法（与 norm/diff.ErrParam 同一错误）。
var ErrParam = diff.ErrParam

// ErrPermission 为角色无权应用该计划。
var ErrPermission = errors.New("plan: permission denied")

// StaleError 记录计划中 id 最小的失配项。
type StaleError struct{ ID int64 }

func (e *StaleError) Error() string { return fmt.Sprintf("plan: stale plan at id %d", e.ID) }

// Is 支持 errors.Is(err, diff.ErrStale)。
func (e *StaleError) Is(target error) bool { return target == diff.ErrStale }

// Item 为计划中的一项，携带 Plan 时刻目标行的版本快照。
type Item struct {
	ID    int64
	Kind  int
	D     *int64 // Insert：Cvt 后的 d；Update：不等列的新值（Cols 含 d 时有效）
	C     []byte // Insert：归一 c；Update：不等列的新值（Cols 含 c 时有效）
	Cols  []int  // Update：不等的列，按 d、c 次序；Insert 为全行，Delete 为空
	Ver   int64  // 生成时目标行版本
	Exist bool   // 生成时目标行是否存在（Insert 恒 false）
}

// Plan 为不可变修复计划（值均为独立拷贝）。
type Plan struct {
	Items []Item
}

// Build 对 [lo,hi) 执行 Compare 并生成计划：
// Missing→Insert，Changed→Update（仅不等列），Extra 仅在 del 为真时→Delete。
// Build 只读，计划是生成时刻的快照。
func Build(e *diff.Engine, lo, hi int64, del bool) (*Plan, error) {
	if e == nil {
		return nil, ErrParam
	}
	rs, err := e.Compare(lo, hi)
	if err != nil {
		return nil, err
	}
	p := &Plan{}
	for _, r := range rs {
		switch r.Kind {
		case diff.Missing:
			p.Items = append(p.Items, Item{
				ID:    r.ID,
				Kind:  KindInsert,
				D:     cloneD(r.SD),
				C:     cloneC(r.SC),
				Cols:  []int{diff.ColD, diff.ColC},
				Exist: false,
			})
		case diff.Changed:
			d, c := cloneD(r.SD), cloneC(r.SC)
			p.Items = append(p.Items, Item{
				ID:    r.ID,
				Kind:  KindUpdate,
				D:     d,
				C:     c,
				Cols:  append([]int(nil), r.Cols...),
				Ver:   r.Ver,
				Exist: true,
			})
		case diff.Extra:
			if del {
				p.Items = append(p.Items, Item{
					ID:    r.ID,
					Kind:  KindDelete,
					Ver:   r.Ver,
					Exist: true,
				})
			}
		}
	}
	return p, nil
}

// Apply 以角色 role 原子应用计划。校验顺序：参数 → 权限 → 版本失配。
// 任一前置条件不满足则整体拒绝且两侧表与版本零改动。
// 返回 (插入数, 更新数, 删除数)；空计划返回 (0,0,0) 且成功。
func Apply(e *diff.Engine, role int, p *Plan) (ins, upd, delN int, err error) {
	if e == nil || p == nil {
		return 0, 0, 0, ErrParam
	}
	if role != RoleRepairer && role != RoleAdmin {
		return 0, 0, 0, ErrPermission
	}
	hasDelete := false
	for _, it := range p.Items {
		if it.Kind == KindDelete {
			hasDelete = true
			break
		}
	}
	if hasDelete && role != RoleAdmin {
		return 0, 0, 0, ErrPermission
	}

	muts := make([]diff.Mutation, 0, len(p.Items))
	for _, it := range p.Items {
		muts = append(muts, diff.Mutation{
			ID:    it.ID,
			Kind:  it.Kind,
			Ver:   it.Ver,
			Exist: it.Exist,
			D:     cloneD(it.D),
			C:     cloneC(it.C),
			Cols:  append([]int(nil), it.Cols...),
		})
		switch it.Kind {
		case KindInsert:
			ins++
		case KindUpdate:
			upd++
		case KindDelete:
			delN++
		default:
			return 0, 0, 0, ErrParam
		}
	}
	id, err := e.TryMutate(muts)
	if err != nil {
		if errors.Is(err, diff.ErrStale) {
			return 0, 0, 0, &StaleError{ID: id}
		}
		return 0, 0, 0, err
	}
	return ins, upd, delN, nil
}

func cloneD(d *int64) *int64 {
	if d == nil {
		return nil
	}
	v := *d
	return &v
}

func cloneC(c []byte) []byte {
	if c == nil {
		return nil
	}
	out := make([]byte, len(c))
	copy(out, c)
	return out
}
