// Package adjrib 维护每个邻居的原始路由表与策略后路由表。
package adjrib

import (
	"errors"

	"ontology/policy"
)

// 哨兵错误，locrib 包重新导出，可用 errors.Is 区分。
var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrPeerNotFound  = errors.New("peer not found")
	ErrPeerExists    = errors.New("peer already exists")
	ErrRouteNotFound = errors.New("route not found")
	ErrPrefixLimit   = errors.New("prefix limit exceeded")
)

// PeerRIB 为单个邻居的 Adj-RIB-In：raw 存原始属性，post 存策略后路由。
type PeerRIB struct {
	localAS  uint32
	as       uint32
	internal bool
	pmax     int
	raw      map[policy.Prefix]policy.Attrs
	post     map[policy.Prefix]policy.Attrs
	terms    []policy.Term
}

// NewPeerRIB 创建邻居路由表；新邻居策略为空（全部拒绝）。
func NewPeerRIB(localAS, as uint32, internal bool, pmax int) *PeerRIB {
	return &PeerRIB{
		localAS:  localAS,
		as:       as,
		internal: internal,
		pmax:     pmax,
		raw:      make(map[policy.Prefix]policy.Attrs),
		post:     make(map[policy.Prefix]policy.Attrs),
	}
}

// Update 先存原始属性（同前缀覆盖），再求策略后路由。
// 新前缀在 raw 已满时报 ErrPrefixLimit；覆盖已有前缀不受限。
func (r *PeerRIB) Update(p policy.Prefix, attrs policy.Attrs) error {
	if _, ok := r.raw[p]; !ok && len(r.raw) >= r.pmax {
		return ErrPrefixLimit
	}
	r.raw[p] = attrs.Clone()
	r.reval(p)
	return nil
}

// reval 用原始属性重算单条前缀的策略后路由：
// asPath 含 localAS 判环路；外部邻居 localPref 先重置为 100；再过导入策略。
func (r *PeerRIB) reval(p policy.Prefix) {
	raw := r.raw[p]
	if policy.HasAS(raw, r.localAS) {
		delete(r.post, p)
		return
	}
	in := raw
	if !r.internal {
		in = raw.Clone()
		in.LocalPref = 100
	}
	out, ok := policy.Eval(r.terms, p, in, r.as)
	if !ok {
		delete(r.post, p)
		return
	}
	r.post[p] = out
}

// Withdraw 删除原始与策略后路由；raw 无此项报 ErrRouteNotFound。
func (r *PeerRIB) Withdraw(p policy.Prefix) error {
	if _, ok := r.raw[p]; !ok {
		return ErrRouteNotFound
	}
	delete(r.raw, p)
	delete(r.post, p)
	return nil
}

// SetPolicy 整体替换策略链，并用 raw 表对全部前缀重新求值。
func (r *PeerRIB) SetPolicy(terms []policy.Term) {
	r.terms = append([]policy.Term(nil), terms...)
	for p := range r.raw {
		r.reval(p)
	}
}

// Post 返回某前缀的策略后路由。
func (r *PeerRIB) Post(p policy.Prefix) (policy.Attrs, bool) {
	a, ok := r.post[p]
	return a, ok
}

// RawPrefixes 返回 raw 表全部前缀（用于 SetPolicy 后同步索引）。
func (r *PeerRIB) RawPrefixes() []policy.Prefix {
	out := make([]policy.Prefix, 0, len(r.raw))
	for p := range r.raw {
		out = append(out, p)
	}
	return out
}

// RawLen 返回 raw 表条数。
func (r *PeerRIB) RawLen() int { return len(r.raw) }
