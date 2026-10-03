package rule

import (
	"errors"
	"sort"
	"strings"
)

// ErrInvalid 表示参数非法（空 id、空键值、越界、枚举非法等）。
var ErrInvalid = errors.New("rule: invalid argument")

// Mode 表示规则的执行模式。
type Mode int

const (
	Enforce Mode = iota // 强制：超限拒绝
	Shadow              // 影子：超限只统计
)

// Pair 是模式中的一个键值对；值为 "*" 表示该键存在即可。
type Pair struct {
	Key   string
	Value string
}

// Rule 是一条描述符限流规则。
type Rule struct {
	ID      string
	Pattern []Pair
	L       int64
	W       int64
	Mode    Mode
}

// Descriptor 是一次请求的描述符：键到实际取值。
type Descriptor map[string]string

// ValidateRule 按 AddRule 的参数规则校验，返回规范化后的规则。
func ValidateRule(id string, pattern []Pair, l, w int64, mode Mode) (*Rule, error) {
	if id == "" {
		return nil, ErrInvalid
	}
	if len(pattern) < 1 || len(pattern) > 3 {
		return nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(pattern))
	cp := make([]Pair, len(pattern))
	for i, p := range pattern {
		if p.Key == "" || p.Value == "" {
			return nil, ErrInvalid
		}
		if _, dup := seen[p.Key]; dup {
			return nil, ErrInvalid
		}
		seen[p.Key] = struct{}{}
		cp[i] = p
	}
	if l < 1 || l > 1_000_000_000 || w < 1 || w > 1_000_000_000 {
		return nil, ErrInvalid
	}
	if !ValidMode(mode) {
		return nil, ErrInvalid
	}
	return &Rule{ID: id, Pattern: cp, L: l, W: w, Mode: mode}, nil
}

// ValidateDescriptor 校验描述符：1..8 对、键值非空、无重复键。
func ValidateDescriptor(desc Descriptor) error {
	if len(desc) < 1 || len(desc) > 8 {
		return ErrInvalid
	}
	for k, v := range desc {
		if k == "" || v == "" {
			return ErrInvalid
		}
	}
	return nil
}

// ValidMode 判断模式枚举是否合法。
func ValidMode(mode Mode) bool {
	return mode == Enforce || mode == Shadow
}

// PatternSig 返回模式的规范化签名（排序后的键值对集合），用于判重。
func PatternSig(pattern []Pair) string {
	pairs := make([]Pair, len(pattern))
	copy(pairs, pattern)
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p.Key)
		b.WriteByte(0)
		b.WriteString(p.Value)
		b.WriteByte(0)
	}
	return b.String()
}

// Match 判断规则是否匹配描述符。
func (r *Rule) Match(desc Descriptor) bool {
	for _, p := range r.Pattern {
		v, ok := desc[p.Key]
		if !ok {
			return false
		}
		if p.Value != "*" && p.Value != v {
			return false
		}
	}
	return true
}

// ExactCount 返回模式中非通配（精确值）对的数量。
func (r *Rule) ExactCount() int {
	n := 0
	for _, p := range r.Pattern {
		if p.Value != "*" {
			n++
		}
	}
	return n
}

// KeySetSig 返回键集合的签名（仅键，排序），用于同键集合分组。
func (r *Rule) KeySetSig() string {
	keys := make([]string, len(r.Pattern))
	for i, p := range r.Pattern {
		keys[i] = p.Key
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

// Values 按 Pattern 中各键的顺序返回描述符里的实际取值（调用前应已 Match）。
func (r *Rule) Values(desc Descriptor) []string {
	vs := make([]string, len(r.Pattern))
	for i, p := range r.Pattern {
		vs[i] = desc[p.Key]
	}
	return vs
}

// Selected 是一条被选中的规则及其择优所需信息。
type Selected struct {
	R *Rule
}

// Select 在已匹配的规则中执行同键集合择优，返回最终生效的规则，
// 结果按 id 字节序排序。
func Select(matched []*Rule) []*Rule {
	groups := make(map[string]*Rule)
	for _, r := range matched {
		g := r.KeySetSig()
		best, ok := groups[g]
		if !ok {
			groups[g] = r
			continue
		}
		rc, bc := r.ExactCount(), best.ExactCount()
		if rc > bc || (rc == bc && r.ID < best.ID) {
			groups[g] = r
		}
	}
	out := make([]*Rule, 0, len(groups))
	for _, r := range groups {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
