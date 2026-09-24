// Package fpred 提供过滤谓词判定、Update 四情形分类与变更/参数校验。
// 本包不依赖项目内其他包。
package fpred

import "errors"

// 变更非法类哨兵错误（与主键冲突/不存在/前像不符互不相同）。
var (
	// ErrInvalidRange：构造谓词时 lo >= hi。
	ErrInvalidRange = errors.New("fpred: require lo < hi")
	// ErrInvalidChange：ID 为空串，或 Update 前后 ID 不同。
	ErrInvalidChange = errors.New("fpred: invalid change")
)

// Row 是源表/视图中的一行。
type Row struct {
	ID  string
	Val int64
}

// Out 是一条变更日志：Add=true 表示 +Row（加入），false 表示 -Row（撤回）。
type Out struct {
	Row Row
	Add bool
}

// Kind 标识源变更种类。
type Kind int

const (
	Insert Kind = iota + 1
	Delete
	Update
)

// Change 是一条源变更：Insert 只填 After，Delete 只填 Before，Update 两者都填。
type Change struct {
	Kind          Kind
	Before, After Row
}

// Pred 是左闭右开谓词 lo <= Val < hi。
type Pred struct {
	lo, hi int64
}

// Case 是分类后应采取的日志动作。
type Case int

const (
	// CaseNone：无输出（两侧都不成立，或两侧成立且 Val 未变）。
	CaseNone Case = iota
	// CaseAdd：仅输出 +After。
	CaseAdd
	// CaseRemove：仅输出 -Before。
	CaseRemove
	// CaseReplace：先输出 -Before 再输出 +After。
	CaseReplace
)

// NewPred 构造谓词；lo >= hi 返回 ErrInvalidRange。
func NewPred(lo, hi int64) (Pred, error) {
	if lo >= hi {
		return Pred{}, ErrInvalidRange
	}
	return Pred{lo: lo, hi: hi}, nil
}

// Holds 判定 P(row) = lo <= Val < hi。
func (p Pred) Holds(r Row) bool { return r.Val >= p.lo && r.Val < p.hi }

// validate 校验单条变更的静态合法性：ID 非空、Update 两侧 ID 一致。
func validate(ch Change) error {
	switch ch.Kind {
	case Insert:
		if ch.After.ID == "" {
			return ErrInvalidChange
		}
	case Delete:
		if ch.Before.ID == "" {
			return ErrInvalidChange
		}
	case Update:
		if ch.Before.ID == "" || ch.After.ID == "" || ch.Before.ID != ch.After.ID {
			return ErrInvalidChange
		}
	default:
		return ErrInvalidChange
	}
	return nil
}

// Classify 校验单条变更并按 P(before)/P(after) 分类为日志动作。
// Insert/Delete 只判定存在的一侧；Update 按四种情形折叠为五态动作。
func Classify(p Pred, ch Change) (Case, error) {
	if err := validate(ch); err != nil {
		return CaseNone, err
	}
	switch ch.Kind {
	case Insert:
		if p.Holds(ch.After) {
			return CaseAdd, nil
		}
		return CaseNone, nil
	case Delete:
		if p.Holds(ch.Before) {
			return CaseRemove, nil
		}
		return CaseNone, nil
	default: // Update
		b, a := p.Holds(ch.Before), p.Holds(ch.After)
		switch {
		case b && a:
			if ch.Before.Val == ch.After.Val {
				return CaseNone, nil // 两侧成立且值未变：视图不变，禁止净零对
			}
			return CaseReplace, nil
		case b && !a:
			return CaseRemove, nil
		case !b && a:
			return CaseAdd, nil
		default:
			return CaseNone, nil
		}
	}
}
