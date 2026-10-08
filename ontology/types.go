// Package ontology 提供通用本体模型：对象类型、链接类型（含方向）、
// 权限模型（存在性、遍历）以及单项 / 批量可达性查询。
package ontology

import "regexp"

// ObjectID 对象标识。
type ObjectID string

// CallerID 调用者标识。
type CallerID string

// ObjectTypeID 对象类型标识。
type ObjectTypeID string

// LinkTypeID 链接类型标识。
type LinkTypeID string

// maxObjectIDLen 对象标识的最大字节长度。
const maxObjectIDLen = 128

// objectIDPattern 合法对象标识：字母或数字开头，后续允许字母、数字、
// '.'、'_'、':'、'-'。
var objectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// Valid 报告对象标识的格式是否合法。
func (id ObjectID) Valid() bool {
	return len(id) >= 1 && len(id) <= maxObjectIDLen && objectIDPattern.MatchString(string(id))
}

// ObjectType 对象类型。
type ObjectType struct {
	ID ObjectTypeID
	// ReachableQueryForbidden 为 true 时，该类型的对象禁止参与可达性查询，
	// 涉及它的请求对一律判定为受限未知。
	ReachableQueryForbidden bool
}

// LinkType 链接类型。链接是有向的：从 From 指向 To。
type LinkType struct {
	ID LinkTypeID
}

// Object 对象实例。
type Object struct {
	ID   ObjectID
	Type ObjectTypeID
}

// Link 有向链接实例：From --Type--> To。
type Link struct {
	Type LinkTypeID
	From ObjectID
	To   ObjectID
}

// Pair 一次可达性请求的起点终点对。
type Pair struct {
	Start ObjectID
	End   ObjectID
}

// TriState 单项可达性判定的三态结果。
type TriState int

const (
	// StateReachable 可达。
	StateReachable TriState = iota
	// StateUnreachable 不可达。
	StateUnreachable
	// StateRestrictedUnknown 受限未知：因类型禁用或存在性权限不足无法判定。
	StateRestrictedUnknown
)

func (s TriState) String() string {
	switch s {
	case StateReachable:
		return "reachable"
	case StateUnreachable:
		return "unreachable"
	case StateRestrictedUnknown:
		return "restricted_unknown"
	}
	return "unknown"
}

// Reason 判定依据（内部可验证、用于审计与测试记录）。
type Reason int

const (
	// ReasonReachable 正常搜索判定为可达。
	ReasonReachable Reason = iota
	// ReasonUnreachable 正常搜索判定为不可达。
	ReasonUnreachable
	// ReasonTypeForbidden 起点或终点所在对象类型被禁止参与可达性查询。
	ReasonTypeForbidden
	// ReasonNoExistence 起点或终点对调用者无存在性权限。
	ReasonNoExistence
)

func (r Reason) String() string {
	switch r {
	case ReasonReachable:
		return "reachable"
	case ReasonUnreachable:
		return "unreachable"
	case ReasonTypeForbidden:
		return "type_forbidden"
	case ReasonNoExistence:
		return "no_existence_permission"
	}
	return "unknown"
}

// ItemResult 单项判定结果：对外三态 + 判定依据。
type ItemResult struct {
	State  TriState
	Reason Reason
}
