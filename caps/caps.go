// Package caps 定义协议特性表、端点 Hello 声明与统一的参数校验。
package caps

const (
	minVersion uint64 = 1
	// MaxVersion 是特性版本参数允许的最大值（无上限特性 maxV=MaxVersion+1）。
	maxFeatureVersion uint64 = 1_000_000_000
	sentinelMaxV      uint64 = maxFeatureVersion + 1
	maxRole           uint8  = 2
	// FeatureCount 是特性编号总数（0..31）。
	FeatureCount = 32
)

// Feature 描述单个特性的生效版本窗口与所需最低角色。
type Feature struct {
	MinV uint64
	MaxV uint64
	Role uint8
}

// Table 是不可变的 32 项特性表，编号 0..31。
type Table struct {
	feat [32]Feature
}

// Hello 是端点声明：版本区间 [Lo,Hi]、支持位集 Sup、必需位集 Req。
type Hello struct {
	Lo  uint64
	Hi  uint64
	Sup uint32
	Req uint32
}

// NewTable 由 32 项特性定义构造不可变特性表。
//
// features 按编号 0..len-1 给出；长度必须恰为 32。
// 每项要求 minV∈[1,10^9]、maxV>minV 且 maxV≤10^9+1、role∈[0,2]。
func NewTable(features []Feature) (*Table, error) {
	if len(features) != FeatureCount {
		return nil, NewError(ReasonInvalid, -1, "table must contain exactly 32 features")
	}
	t := &Table{}
	for i, f := range features {
		if f.MinV < minVersion || f.MinV > maxFeatureVersion {
			return nil, NewError(ReasonInvalid, i, "minV out of range")
		}
		if f.MaxV <= f.MinV || f.MaxV > sentinelMaxV {
			return nil, NewError(ReasonInvalid, i, "maxV must satisfy minV < maxV <= 10^9+1")
		}
		if f.Role > maxRole {
			return nil, NewError(ReasonInvalid, i, "role out of range")
		}
		t.feat[i] = f
	}
	return t, nil
}

// At 返回编号 i 的特性定义，越界返回 ok=false。
func (t *Table) At(i uint8) (Feature, bool) {
	if int(i) >= FeatureCount {
		return Feature{}, false
	}
	return t.feat[i], true
}

// AvailableAt 判定特性 i 在版本 v 是否可用：minV ≤ v < maxV。
func (t *Table) AvailableAt(i uint8, v uint64) bool {
	if int(i) >= FeatureCount {
		return false
	}
	f := t.feat[i]
	return v >= f.MinV && v < f.MaxV
}

// ValidateHello 校验 Hello 内部一致性：1 ≤ lo ≤ hi ≤ 10^9，req⊆sup。
func ValidateHello(h Hello) error {
	if h.Lo < minVersion || h.Lo > maxFeatureVersion {
		return NewError(ReasonInvalid, -1, "lo out of range")
	}
	if h.Hi < minVersion || h.Hi > maxFeatureVersion {
		return NewError(ReasonInvalid, -1, "hi out of range")
	}
	if h.Lo > h.Hi {
		return NewError(ReasonInvalid, -1, "lo > hi")
	}
	if h.Req&^h.Sup != 0 {
		return NewError(ReasonInvalid, -1, "req is not a subset of sup")
	}
	return nil
}

// ValidateRole 校验角色在 0..2。
func ValidateRole(r uint8) error {
	if r > maxRole {
		return NewError(ReasonInvalid, -1, "role out of range")
	}
	return nil
}

// ValidateFeature 校验特性编号在 0..31。
func ValidateFeature(f uint8) error {
	if int(f) >= FeatureCount {
		return NewError(ReasonInvalid, int(f), "feature out of range")
	}
	return nil
}

// EmptyHello 是服务端初始空声明：lo=hi=1、sup=req=0。
func EmptyHello() Hello {
	return Hello{Lo: 1, Hi: 1, Sup: 0, Req: 0}
}

// Bits 返回位集 b 中置位的特性编号（按编号升序）。
func Bits(b uint32) []uint8 {
	out := make([]uint8, 0, FeatureCount)
	for i := 0; i < FeatureCount; i++ {
		if b&(1<<uint(i)) != 0 {
			out = append(out, uint8(i))
		}
	}
	return out
}

// BitSet 构造单个特性的位掩码。
func BitSet(i uint8) uint32 {
	return 1 << i
}
