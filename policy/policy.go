// Package policy 描述每个头部的语义策略：是否列表型、重复时单值访问
// 如何归并、名字是否大小写敏感。本包只做判定与归并，不持有数据。
package policy

import (
	"errors"
	"strings"

	"ontology/listval"
	"ontology/token"
)

// ErrDuplicate 表示策略为 DupError 的头部出现了重复，单值访问报错。
var ErrDuplicate = errors.New("policy: duplicate header under error policy")

// Dup 是重复出现时的单值归并策略。
type Dup int

const (
	DupFirst Dup = iota // 取首个
	DupLast             // 取末个
	DupMerge            // 按列表语义合并（", " 连接）
	DupError            // 报错
)

// Policy 是一个头部的语义策略。
type Policy struct {
	List        bool   // 值是否为逗号分隔列表
	Dup         Dup    // 重复时单值访问的归并方式
	CaseSens    bool   // 名字是否大小写敏感（默认 false，规范化为统一形态）
	CanonicalTo string // 可选：规范化时使用的名字形态，空则自动推导
}

// Registry 按名字（小写键）登记策略，未登记的用 Default。
type Registry struct {
	Default Policy
	byName  map[string]Policy
}

// NewRegistry 创建以 def 为默认策略的登记表。
func NewRegistry(def Policy) *Registry {
	return &Registry{Default: def, byName: map[string]Policy{}}
}

// Register 为若干头部名登记策略（名字大小写不敏感地登记）。
func (r *Registry) Register(p Policy, names ...string) {
	for _, n := range names {
		r.byName[strings.ToLower(n)] = p
	}
}

// For 返回某名字的策略。
func (r *Registry) For(name string) Policy {
	if p, ok := r.byName[strings.ToLower(name)]; ok {
		return p
	}
	return r.Default
}

// Canonical 把名字规范化为统一形态：每个连字符分隔段首字母大写、其余小写。
// 输入必须先通过 token.ValidName。大小写敏感策略下保留原名。
func Canonical(name string, p Policy) string {
	if p.CaseSens {
		return name
	}
	if p.CanonicalTo != "" {
		return p.CanonicalTo
	}
	var b strings.Builder
	b.Grow(len(name))
	upper := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '-':
			upper = true
			b.WriteByte(c)
		case upper && c >= 'a' && c <= 'z':
			b.WriteByte(c - 32)
			upper = false
		case !upper && c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
		default:
			b.WriteByte(c)
			upper = false
		}
	}
	return b.String()
}

// Single 按策略把同名的多个值归并为单值。
func Single(p Policy, values []string) (string, error) {
	switch {
	case len(values) == 0:
		return "", nil
	case len(values) == 1 || p.Dup == DupFirst:
		return values[0], nil
	case p.Dup == DupLast:
		return values[len(values)-1], nil
	case p.Dup == DupMerge:
		return strings.Join(values, ", "), nil
	default:
		return "", ErrDuplicate
	}
}

// ValidateValue 按策略校验一个值：先做字符安全检查，列表型再做切分校验。
func ValidateValue(p Policy, v string) error {
	if err := token.ValidateValue(v); err != nil {
		return err
	}
	if p.List {
		_, err := listval.Split(v)
		return err
	}
	return nil
}
