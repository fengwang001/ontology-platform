package ontology

import (
	"errors"
	"fmt"
)

// 标识类型。
type (
	// PrincipalID 权限主体标识。
	PrincipalID string
	// GroupID 权限组标识。
	GroupID string
	// ObjectTypeID 对象类型标识。
	ObjectTypeID string
	// LinkTypeID 链接类型标识。
	LinkTypeID string
	// ObjectID 对象（图节点）标识。
	ObjectID string
)

// DeclValue 是一条权限声明的取值。
type DeclValue int8

const (
	// DeclAllow 允许遍历。
	DeclAllow DeclValue = iota + 1
	// DeclDeny 拒绝遍历。
	DeclDeny
)

func (v DeclValue) String() string {
	switch v {
	case DeclAllow:
		return "allow"
	case DeclDeny:
		return "deny"
	default:
		return fmt.Sprintf("DeclValue(%d)", int8(v))
	}
}

// Decision 是一次覆盖判定的结果。
type Decision int8

const (
	// DecisionAllow 允许遍历。
	DecisionAllow Decision = iota + 1
	// DecisionDeny 拒绝遍历。
	DecisionDeny
	// DecisionAmbiguous 因同优先级声明矛盾且合并规则无法给出单一结果而无法判定。
	DecisionAmbiguous
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionAmbiguous:
		return "ambiguous"
	default:
		return fmt.Sprintf("Decision(%d)", int8(d))
	}
}

// Layer 标识声明所在的层。
type Layer int8

const (
	// LayerLink 链接类型层。
	LayerLink Layer = iota + 1
	// LayerObject 对象类型层。
	LayerObject
	// LayerDefault 两层均未声明时的默认拒绝。
	LayerDefault
)

func (l Layer) String() string {
	switch l {
	case LayerLink:
		return "link"
	case LayerObject:
		return "object"
	case LayerDefault:
		return "default"
	default:
		return fmt.Sprintf("Layer(%d)", int8(l))
	}
}

// ConflictPolicy 决定同优先级权限组声明矛盾时的合并策略。
type ConflictPolicy int

const (
	// DenyOverrides 规范默认策略：同优先级矛盾时拒绝优先于允许，
	// 合并总能给出单一结果，因此不会产生歧义判定。
	DenyOverrides ConflictPolicy = iota
	// StrictConflict 严格策略：同优先级矛盾时合并无法给出单一结果，
	// 判定为歧义。用于审计场景以及区分“不可达”与“覆盖歧义”。
	StrictConflict
)

// 错误定义。
var (
	// ErrInvalidPrincipal 权限主体标识不合法。
	ErrInvalidPrincipal = errors.New("ontology: invalid principal id")
	// ErrUnknownObject 路径端点对象不存在。
	ErrUnknownObject = errors.New("ontology: unknown object")
	// ErrInvalidCost 链接代价必须为正数。
	ErrInvalidCost = errors.New("ontology: link cost must be positive")
)

// maxPrincipalIDLen 权限主体标识的最大长度。
const maxPrincipalIDLen = 128

// ValidatePrincipalID 校验权限主体标识是否合法。
// 合法标识非空、长度不超过上限，且仅由字母、数字及 ._-:@ 组成。
func ValidatePrincipalID(p PrincipalID) error {
	if len(p) == 0 || len(p) > maxPrincipalIDLen {
		return fmt.Errorf("%w: %q", ErrInvalidPrincipal, string(p))
	}
	for _, r := range string(p) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':', r == '@':
		default:
			return fmt.Errorf("%w: %q", ErrInvalidPrincipal, string(p))
		}
	}
	return nil
}
