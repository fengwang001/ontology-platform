package ontology

import "errors"

// Reason 用稳定的机器可读字符串区分不同的拒绝原因。
type Reason string

const (
	// ReasonEmptyID：提交 id 为空字符串。
	ReasonEmptyID Reason = "empty id"
	// ReasonDuplicateID：提交 id 已登记。
	ReasonDuplicateID Reason = "duplicate id"
	// ReasonTooManyParents：父列表超过 8 个。
	ReasonTooManyParents Reason = "too many parents"
	// ReasonDuplicateParent：父列表含重复 id（取第一次出现重复的那一个）。
	ReasonDuplicateParent Reason = "duplicate parent"
	// ReasonParentNotRegistered：父 id 尚未登记。
	ReasonParentNotRegistered Reason = "parent not registered"
	// ReasonGenUnknownID：Gen 查询的 id 未登记。
	ReasonGenUnknownID Reason = "gen: unknown id"
	// ReasonIsAncestorUnknownA：IsAncestor 的第一个参数 a 未登记。
	ReasonIsAncestorUnknownA Reason = "isancestor: unknown a"
	// ReasonIsAncestorUnknownB：IsAncestor 的第二个参数 b 未登记。
	ReasonIsAncestorUnknownB Reason = "isancestor: unknown b"
	// ReasonMergeBasesUnknownA：MergeBases 的第一个参数 a 未登记。
	ReasonMergeBasesUnknownA Reason = "mergebases: unknown a"
	// ReasonMergeBasesUnknownB：MergeBases 的第二个参数 b 未登记。
	ReasonMergeBasesUnknownB Reason = "mergebases: unknown b"
)

// Error 携带可区分的拒绝原因。可通过 errors.As 提取。
type Error struct {
	Reason Reason
	// ID 为触发错误的提交 id（存在重复父时为该父 id）。
	ID string
	// Index 为父列表中触发错误的最小下标（存在时）。
	Index int
	msg   string
}

func (e *Error) Error() string { return e.msg }

func newError(reason Reason, id string, index int) *Error {
	return &Error{Reason: reason, ID: id, Index: index, msg: string(reason)}
}

// AsError 提取 *Error，便于测试断言原因。
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
