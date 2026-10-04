package policy

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrPeerExists      = errors.New("peer already exists")
	ErrPeerNotFound    = errors.New("peer not found")
	ErrRouteNotFound   = errors.New("route not found")
	ErrPrefixLimit     = errors.New("prefix limit exceeded")
)

const (
	ActionAccept = iota
	ActionReject
	ActionNext
)

const (
	NoExport    uint32 = 0xFFFFFF01
	NoAdvertise uint32 = 0xFFFFFF02
	DefaultPref        = uint32(100)
)

type Prefix struct {
	Addr uint32
	Len  uint8
}

type Attrs struct {
	ASPath      []uint32
	LocalPref   uint32
	MED         uint32
	Origin      uint8
	Communities []uint32
}

type Match struct {
	HasPrefix    bool
	Prefix       Prefix
	GE           uint8
	LE           uint8
	HasCommunity bool
	Community    uint32
	HasAS        bool
	AS           uint32
}

type Action struct {
	Kind         int
	SetLocalPref bool
	LocalPref    uint32
	AddCommunity bool
	Community    uint32
	PrependCount uint8
}

type Term struct {
	Match  Match
	Action Action
}

type Peer struct {
	ID       int
	AS       uint32
	RouterID uint32
	Internal bool
	Policy   []Term
}

// mask 保留地址前 l 位。
func mask(addr uint32, l uint8) uint32 {
	if l == 0 {
		return 0
	}
	return addr & (^uint32(0) << (32 - l))
}

// ValidatePrefix 校验长度范围与主机位为零。
func ValidatePrefix(p Prefix) error {
	if p.Len > 32 {
		return ErrInvalidArgument
	}
	if p.Addr != mask(p.Addr, p.Len) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateAttrs 校验各属性取值范围与团体唯一性，返回团体升序的规范化副本。
func ValidateAttrs(a Attrs) (Attrs, error) {
	out := a.Clone()
	if len(out.ASPath) > 64 {
		return out, ErrInvalidArgument
	}
	if out.Origin > 2 {
		return out, ErrInvalidArgument
	}
	if len(out.Communities) > 32 {
		return out, ErrInvalidArgument
	}
	seen := make(map[uint32]struct{}, len(out.Communities))
	for _, c := range out.Communities {
		if _, dup := seen[c]; dup {
			return out, ErrInvalidArgument
		}
		seen[c] = struct{}{}
	}
	sort.Slice(out.Communities, func(i, j int) bool { return out.Communities[i] < out.Communities[j] })
	if out.Communities == nil {
		out.Communities = []uint32{}
	}
	if out.ASPath == nil {
		out.ASPath = []uint32{}
	}
	return out, nil
}

// ValidateTerms 校验 0~64 条条款及前缀条件 l<=ge<=le<=32。
func ValidateTerms(terms []Term) error {
	if len(terms) > 64 {
		return ErrInvalidArgument
	}
	for _, term := range terms {
		m := term.Match
		if m.HasPrefix {
			if err := ValidatePrefix(m.Prefix); err != nil {
				return err
			}
			if m.GE < m.Prefix.Len || m.GE > m.LE || m.LE > 32 {
				return ErrInvalidArgument
			}
		}
		switch term.Action.Kind {
		case ActionAccept, ActionReject, ActionNext:
		default:
			return ErrInvalidArgument
		}
		if term.Action.PrependCount > 8 {
			return ErrInvalidArgument
		}
	}
	return nil
}

func matchPrefix(m Match, p Prefix) bool {
	return p.Len >= m.GE && p.Len <= m.LE && mask(p.Addr, m.Prefix.Len) == m.Prefix.Addr
}

func termMatches(m Match, p Prefix, a Attrs) bool {
	if m.HasPrefix && !matchPrefix(m, p) {
		return false
	}
	if m.HasCommunity {
		found := false
		for _, c := range a.Communities {
			if c == m.Community {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if m.HasAS {
		found := false
		for _, as := range a.ASPath {
			if as == m.AS {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func applyAction(a Attrs, act Action, peerAS uint32) Attrs {
	out := a.Clone()
	if act.SetLocalPref {
		out.LocalPref = act.LocalPref
	}
	if act.AddCommunity && len(out.Communities) < 32 {
		present := false
		for _, c := range out.Communities {
			if c == act.Community {
				present = true
				break
			}
		}
		if !present {
			out.Communities = append(out.Communities, act.Community)
			sort.Slice(out.Communities, func(i, j int) bool { return out.Communities[i] < out.Communities[j] })
		}
	}
	if act.PrependCount > 0 {
		room := 64 - len(out.ASPath)
		n := int(act.PrependCount)
		if n > room {
			n = room
		}
		if n > 0 {
			out.ASPath = append(append(make([]uint32, 0, len(out.ASPath)+n), repeatAS(peerAS, n)...), out.ASPath...)
		}
	}
	return out
}

func repeatAS(as uint32, n int) []uint32 {
	s := make([]uint32, n)
	for i := range s {
		s[i] = as
	}
	return s
}

// Apply 执行导入策略链。external 时先将 localPref 重置为 100；
// asPath 含 localAS 判环路。ok=false 表示无策略后路由。
func Apply(raw Attrs, p Prefix, terms []Term, localAS, peerAS uint32, external bool) (Attrs, bool) {
	cur := raw.Clone()
	for _, as := range cur.ASPath {
		if as == localAS {
			return Attrs{}, false
		}
	}
	if external {
		cur.LocalPref = DefaultPref
	}
	for _, term := range terms {
		if !termMatches(term.Match, p, cur) {
			continue
		}
		cur = applyAction(cur, term.Action, peerAS)
		if term.Action.Kind == ActionAccept {
			return cur, true
		}
		if term.Action.Kind == ActionReject {
			return Attrs{}, false
		}
	}
	return Attrs{}, false
}

// AttrsEqual 逐字段比较（含切片顺序）。
func AttrsEqual(a, b Attrs) bool {
	return a.LocalPref == b.LocalPref && a.MED == b.MED && a.Origin == b.Origin &&
		eqUint32(a.ASPath, b.ASPath) && eqUint32(a.Communities, b.Communities)
}

func eqUint32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Clone 深拷贝。
func (a Attrs) Clone() Attrs {
	out := a
	if a.ASPath != nil {
		out.ASPath = append([]uint32(nil), a.ASPath...)
	}
	if a.Communities != nil {
		out.Communities = append([]uint32(nil), a.Communities...)
	}
	return out
}
