// Package clearance 在授权登记簿上提供只读的可播判定。
package clearance

import (
	"errors"

	"ontology/grant"
)

// 判定结果三选一。
const (
	VerdictAllow     = "allow"
	VerdictExclusive = "exclusive"
	VerdictNoLicense = "no_license"
)

var (
	ErrInvalidArg  = errors.New("clearance: invalid argument")
	ErrUnknownNode = errors.New("clearance: unknown node")
	ErrNotLeaf     = errors.New("clearance: node is not a leaf")
)

// Decision 是一次可播判定结果。
type Decision struct {
	Verdict string
	// ExclusiveID 仅在 Verdict == VerdictExclusive 时有值。
	ExclusiveID string
}

// Engine 复用 grant.Registry 的同一份状态做只读判定。
type Engine struct {
	reg *grant.Registry
}

// New 基于登记簿创建判定器。
func New(reg *grant.Registry) *Engine {
	return &Engine{reg: reg}
}

// CanPlay 判定 (title, licensee, leaf) 在时刻 t 是否可播。
func (e *Engine) CanPlay(title, licensee, leaf string, t int64) (Decision, error) {
	// 参数非法 > 地域未知 > 非叶节点。
	if title == "" || licensee == "" || leaf == "" || t < 0 {
		return Decision{}, ErrInvalidArg
	}
	active, err := e.reg.Active(title, leaf, t)
	if err != nil {
		switch {
		case errors.Is(err, grant.ErrUnknownRegion):
			return Decision{}, ErrUnknownNode
		case errors.Is(err, grant.ErrBadExcludes):
			return Decision{}, ErrNotLeaf
		default:
			return Decision{}, err
		}
	}

	own := false
	exclusiveID := ""
	for _, g := range active {
		if g.Licensee == licensee {
			own = true
			continue
		}
		if g.Exclusive {
			if exclusiveID == "" || g.ID < exclusiveID {
				exclusiveID = g.ID
			}
		}
	}
	switch {
	case own:
		return Decision{Verdict: VerdictAllow}, nil
	case exclusiveID != "":
		return Decision{Verdict: VerdictExclusive, ExclusiveID: exclusiveID}, nil
	default:
		return Decision{Verdict: VerdictNoLicense}, nil
	}
}

// Holders 按 id 字节序列出此刻覆盖此叶的全部生效授权。
func (e *Engine) Holders(title, leaf string, t int64) ([]grant.Grant, error) {
	if title == "" || leaf == "" || t < 0 {
		return nil, ErrInvalidArg
	}
	active, err := e.reg.Active(title, leaf, t)
	if err != nil {
		switch {
		case errors.Is(err, grant.ErrUnknownRegion):
			return nil, ErrUnknownNode
		case errors.Is(err, grant.ErrBadExcludes):
			return nil, ErrNotLeaf
		default:
			return nil, err
		}
	}
	return active, nil
}
