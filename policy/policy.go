// Package policy 定义路由属性、前缀与导入策略链，并提供策略求值。
package policy

import "slices"

const (
	NoExport    uint32 = 0xFFFFFF01
	NoAdvertise uint32 = 0xFFFFFF02

	MaxAsPath      = 64
	MaxCommunities = 32
	MaxTerms       = 64
	MaxPrepend     = 8
)

// Prefix 为 32 位地址前缀，主机位必须为零。
type Prefix struct {
	Addr uint32
	Len  uint8
}

// Mask 返回前 l 位的掩码。
func Mask(l uint8) uint32 {
	if l == 0 {
		return 0
	}
	return ^uint32(0) << (32 - l)
}

// ValidPrefix 校验长度范围与主机位为零。
func ValidPrefix(p Prefix) bool {
	return p.Len <= 32 && p.Addr&^Mask(p.Len) == 0
}

// Attrs 为路由属性。
type Attrs struct {
	AsPath      []uint32
	LocalPref   uint32
	Med         uint32
	Origin      uint8
	Communities []uint32
}

// ValidAttrs 校验 asPath 长度、origin 取值与团体数量及互异性。
func ValidAttrs(a Attrs) bool {
	if len(a.AsPath) > MaxAsPath || a.Origin > 2 || len(a.Communities) > MaxCommunities {
		return false
	}
	seen := make(map[uint32]struct{}, len(a.Communities))
	for _, c := range a.Communities {
		if _, ok := seen[c]; ok {
			return false
		}
		seen[c] = struct{}{}
	}
	return true
}

// Clone 深拷贝属性。
func (a Attrs) Clone() Attrs {
	out := a
	out.AsPath = slices.Clone(a.AsPath)
	out.Communities = slices.Clone(a.Communities)
	return out
}

// EqualAttrs 逐字段比较（asPath 与 communities 保序）。
func EqualAttrs(a, b Attrs) bool {
	return a.LocalPref == b.LocalPref &&
		a.Med == b.Med &&
		a.Origin == b.Origin &&
		slices.Equal(a.AsPath, b.AsPath) &&
		slices.Equal(a.Communities, b.Communities)
}

// HasCommunity 判断属性是否含指定团体。
func HasCommunity(a Attrs, c uint32) bool {
	return slices.Contains(a.Communities, c)
}

// HasAS 判断 asPath 是否含指定 AS。
func HasAS(a Attrs, as uint32) bool {
	return slices.Contains(a.AsPath, as)
}

// Action 为条款动作。
type Action uint8

const (
	ActionAccept Action = iota
	ActionReject
	ActionNext
)

// Valid 判断动作取值合法。
func (a Action) Valid() bool { return a <= ActionNext }

// PrefixCond 前缀条件：命中长度在 [Ge,Le] 内且前 Len 位与 Addr 相同的路由。
type PrefixCond struct {
	Addr uint32
	Len  uint8
	Ge   uint8
	Le   uint8
}

// Match 为三种可选条件的与；三者皆缺则恒命中。
type Match struct {
	Prefix    *PrefixCond
	Community *uint32
	AS        *uint32
}

// Mods 为条款携带的属性修改。
type Mods struct {
	LocalPref    *uint32
	AddCommunity *uint32
	Prepend      int
}

// Term 为一条策略条款。
type Term struct {
	Match  Match
	Action Action
	Mods   Mods
}

// ValidTerms 校验策略链长度与每条条款的参数。
func ValidTerms(terms []Term) bool {
	if len(terms) > MaxTerms {
		return false
	}
	for _, t := range terms {
		if !t.Action.Valid() {
			return false
		}
		if pc := t.Match.Prefix; pc != nil {
			if !(pc.Len <= pc.Ge && pc.Ge <= pc.Le && pc.Le <= 32) {
				return false
			}
			if !ValidPrefix(Prefix{Addr: pc.Addr, Len: pc.Len}) {
				return false
			}
		}
		if t.Mods.Prepend < 0 || t.Mods.Prepend > MaxPrepend {
			return false
		}
	}
	return true
}

// Matches 判断条款匹配条件是否命中。
func (m Match) Matches(p Prefix, a Attrs) bool {
	if pc := m.Prefix; pc != nil {
		if p.Len < pc.Ge || p.Len > pc.Le {
			return false
		}
		if p.Addr&Mask(pc.Len) != pc.Addr&Mask(pc.Len) {
			return false
		}
	}
	if c := m.Community; c != nil && !HasCommunity(a, *c) {
		return false
	}
	if as := m.AS; as != nil && !HasAS(a, *as) {
		return false
	}
	return true
}

func applyMods(a Attrs, m Mods, peerAS uint32) Attrs {
	if m.LocalPref != nil {
		a.LocalPref = *m.LocalPref
	}
	if m.AddCommunity != nil {
		c := *m.AddCommunity
		if !HasCommunity(a, c) && len(a.Communities) < MaxCommunities {
			a.Communities = append(a.Communities, c)
		}
	}
	if m.Prepend > 0 {
		n := m.Prepend
		if room := MaxAsPath - len(a.AsPath); n > room {
			n = room
		}
		if n > 0 {
			path := make([]uint32, 0, len(a.AsPath)+n)
			for i := 0; i < n; i++ {
				path = append(path, peerAS)
			}
			a.AsPath = append(path, a.AsPath...)
		}
	}
	return a
}

// Eval 按序求值策略链：命中 accept/reject 即终止，命中 next 应用修改后继续，
// 后续条款看到修改后的属性；无条款终止时默认拒绝。peerAS 用于 asPath 前插。
func Eval(terms []Term, p Prefix, attrs Attrs, peerAS uint32) (Attrs, bool) {
	cur := attrs.Clone()
	for _, t := range terms {
		if !t.Match.Matches(p, cur) {
			continue
		}
		cur = applyMods(cur, t.Mods, peerAS)
		switch t.Action {
		case ActionAccept:
			return cur, true
		case ActionReject:
			return cur, false
		}
	}
	return cur, false
}
