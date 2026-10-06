package ontology

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// TypeKind 标识公开类型的形态。
type TypeKind int

const (
	KindNumber TypeKind = iota
	KindString
	KindBoolean
	KindNull
	KindUndefined
	KindNumberLiteral
	KindStringLiteral
	KindBooleanLiteral
	KindObject
	KindUnion
)

// Type 是调用方使用的类型节点。提交给分析器后视为不可变。
//
// 各形态使用的字段：
//   - 原子类型 KindNumber/KindString/KindBoolean/KindNull/KindUndefined：无额外字段
//   - KindNumberLiteral：NumValue
//   - KindStringLiteral：StrValue
//   - KindBooleanLiteral：BoolValue
//   - KindObject：Props（属性名到类型的映射，集合有限）
//   - KindUnion：Members（嵌套联合在规范化时展开）
type Type struct {
	K         TypeKind
	NumValue  float64
	StrValue  string
	BoolValue bool
	Props     map[string]*Type
	Members   []*Type
	// DuplicateProps 记录对象字面量中重复出现的属性名。
	// 常规构造为空；出现重复时该类型被判定为参数非法。
	DuplicateProps []string
}

// ---- 公开构造器 ----

func TNumber() *Type          { return &Type{K: KindNumber} }
func TString() *Type          { return &Type{K: KindString} }
func TBoolean() *Type         { return &Type{K: KindBoolean} }
func TNull() *Type            { return &Type{K: KindNull} }
func TUndefined() *Type       { return &Type{K: KindUndefined} }
func TNumLit(v float64) *Type { return &Type{K: KindNumberLiteral, NumValue: v} }
func TStrLit(v string) *Type  { return &Type{K: KindStringLiteral, StrValue: v} }
func TBoolLit(v bool) *Type   { return &Type{K: KindBooleanLiteral, BoolValue: v} }
func TTrue() *Type            { return TBoolLit(true) }
func TFalse() *Type           { return TBoolLit(false) }
func TNever() *Type           { return TUnion() }

// TObject 以属性映射构造对象类型；映射会被复制。
func TObject(props map[string]*Type) *Type {
	cp := make(map[string]*Type, len(props))
	for k, v := range props {
		cp[k] = v
	}
	return &Type{K: KindObject, Props: cp}
}

// TUnion 构造联合类型；嵌套联合在规范化时展开，空参数表示永不类型。
func TUnion(members ...*Type) *Type {
	return &Type{K: KindUnion, Members: append([]*Type(nil), members...)}
}

// ---- 内部规范化表示 ----

// normType 是规范化联合：成员有序、去重、字面量已被对应原子吸收。
// 空成员集合即永不类型；单成员等同该成员（仅存储形式上仍是集合）。
type normType struct {
	members []leaf
}

type leafKind uint8

const (
	lkNumber leafKind = iota
	lkString
	lkTrue
	lkFalse
	lkNull
	lkUndefined
	lkNumLit
	lkStrLit
	lkObject
)

// leaf 是规范化联合的一个成员。对象成员的属性值也全部规范化。
type leaf struct {
	kind  leafKind
	num   float64
	str   string
	props map[string]*normType
}

func normalize(t *Type) (*normType, error) {
	leaves, err := collectLeaves(t, nil)
	if err != nil {
		return nil, err
	}
	return canonical(leaves), nil
}

func collectLeaves(t *Type, seen map[*Type]bool) ([]leaf, error) {
	if t == nil {
		return nil, fmt.Errorf("类型为 nil")
	}
	switch t.K {
	case KindNumber:
		return []leaf{{kind: lkNumber}}, nil
	case KindString:
		return []leaf{{kind: lkString}}, nil
	case KindBoolean:
		return []leaf{{kind: lkTrue}, {kind: lkFalse}}, nil
	case KindNull:
		return []leaf{{kind: lkNull}}, nil
	case KindUndefined:
		return []leaf{{kind: lkUndefined}}, nil
	case KindNumberLiteral:
		if math.IsNaN(t.NumValue) {
			return nil, fmt.Errorf("数字字面量不能为 NaN")
		}
		return []leaf{{kind: lkNumLit, num: t.NumValue}}, nil
	case KindStringLiteral:
		return []leaf{{kind: lkStrLit, str: t.StrValue}}, nil
	case KindBooleanLiteral:
		if t.BoolValue {
			return []leaf{{kind: lkTrue}}, nil
		}
		return []leaf{{kind: lkFalse}}, nil
	case KindObject:
		if t.Props == nil {
			return nil, fmt.Errorf("对象类型缺少属性映射")
		}
		if dup := firstDuplicateProp(t.DuplicateProps); dup != "" {
			return nil, fmt.Errorf("对象类型存在重复属性名 %q", dup)
		}
		keys := make([]string, 0, len(t.Props))
		for k := range t.Props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		props := make(map[string]*normType, len(keys))
		for _, k := range keys {
			nt, err := normalize(t.Props[k])
			if err != nil {
				return nil, fmt.Errorf("对象属性 %q 的类型非法: %w", k, err)
			}
			props[k] = nt
		}
		return []leaf{{kind: lkObject, props: props}}, nil
	case KindUnion:
		if seen == nil {
			seen = map[*Type]bool{}
		}
		if seen[t] {
			return nil, fmt.Errorf("联合类型存在循环引用")
		}
		seen[t] = true
		var out []leaf
		for _, m := range t.Members {
			ms, err := collectLeaves(m, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, ms...)
		}
		delete(seen, t)
		return out, nil
	default:
		return nil, fmt.Errorf("未知的类型形态 %d", t.K)
	}
}

// firstDuplicateProp 返回第一个被标记为重复的属性名（确定性次序）。
func firstDuplicateProp(dups []string) string {
	if len(dups) == 0 {
		return ""
	}
	cp := append([]string(nil), dups...)
	sort.Strings(cp)
	return cp[0]
}

// canonical 执行去重、字面量吸收与排序。
func canonical(leaves []leaf) *normType {
	has := map[string]bool{}
	var uniq []leaf
	for _, lf := range leaves {
		key := leafKey(lf)
		if !has[key] {
			has[key] = true
			uniq = append(uniq, lf)
		}
	}

	absorbed := map[string]bool{}
	for _, lf := range uniq {
		switch lf.kind {
		case lkNumber:
			for _, other := range uniq {
				if other.kind == lkNumLit {
					absorbed[leafKey(other)] = true
				}
			}
		case lkString:
			for _, other := range uniq {
				if other.kind == lkStrLit {
					absorbed[leafKey(other)] = true
				}
			}
		}
	}

	var kept []leaf
	for _, lf := range uniq {
		if !absorbed[leafKey(lf)] {
			kept = append(kept, lf)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return leafKey(kept[i]) < leafKey(kept[j]) })
	return &normType{members: kept}
}

func leafKey(l leaf) string {
	switch l.kind {
	case lkNumber:
		return "0:number"
	case lkString:
		return "1:string"
	case lkTrue:
		return "2:true"
	case lkFalse:
		return "3:false"
	case lkNull:
		return "4:null"
	case lkUndefined:
		return "5:undefined"
	case lkNumLit:
		return "6:n:" + strconv.FormatFloat(l.num, 'g', -1, 64)
	case lkStrLit:
		return "7:s:" + l.str
	case lkObject:
		keys := make([]string, 0, len(l.props))
		for k := range l.props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("8:o:{")
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(k))
			b.WriteByte(':')
			b.WriteString(normKey(l.props[k]))
		}
		b.WriteByte('}')
		return b.String()
	}
	return "9:?"
}

func normKey(n *normType) string {
	parts := make([]string, len(n.members))
	for i, lf := range n.members {
		parts[i] = leafKey(lf)
	}
	return "[" + strings.Join(parts, "|") + "]"
}

func (n *normType) isNever() bool { return n != nil && len(n.members) == 0 }

// assignable 报告所赋类型的每个归一化成员是否都属于声明类型。
// 声明类型中原子 number/string 吸收对应字面量，布尔声明吸收 true/false。
func assignable(value, declared *normType) bool {
	dset := map[string]bool{}
	hasNumber, hasString, hasBool := false, false, false
	for _, lf := range declared.members {
		dset[leafKey(lf)] = true
		switch lf.kind {
		case lkNumber:
			hasNumber = true
		case lkString:
			hasString = true
		case lkTrue, lkFalse:
			hasBool = true
		}
	}
	for _, lf := range value.members {
		if dset[leafKey(lf)] {
			continue
		}
		switch lf.kind {
		case lkNumLit:
			if hasNumber {
				continue
			}
		case lkStrLit:
			if hasString {
				continue
			}
		case lkTrue, lkFalse:
			if hasBool {
				continue
			}
		}
		return false
	}
	return true
}

// unionNorm 求两个规范化类型的并集（逐变量汇合时使用）。
func unionNorm(a, b *normType) *normType {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	all := make([]leaf, 0, len(a.members)+len(b.members))
	all = append(all, a.members...)
	all = append(all, b.members...)
	return canonical(all)
}

// ---- 对外类型还原 ----

func toPublic(n *normType) *Type {
	if n.isNever() {
		return TNever()
	}
	if len(n.members) == 1 {
		return leafToPublic(n.members[0])
	}
	ms := make([]*Type, len(n.members))
	for i, lf := range n.members {
		ms[i] = leafToPublic(lf)
	}
	return TUnion(ms...)
}

func leafToPublic(l leaf) *Type {
	switch l.kind {
	case lkNumber:
		return TNumber()
	case lkString:
		return TString()
	case lkTrue:
		return TTrue()
	case lkFalse:
		return TFalse()
	case lkNull:
		return TNull()
	case lkUndefined:
		return TUndefined()
	case lkNumLit:
		return TNumLit(l.num)
	case lkStrLit:
		return TStrLit(l.str)
	case lkObject:
		props := make(map[string]*Type, len(l.props))
		for k, v := range l.props {
			props[k] = toPublic(v)
		}
		return TObject(props)
	}
	return nil
}

// Normalize 返回归一化后的独立副本；err 描述参数非法的原因。
func (t *Type) Normalize() (*Type, error) {
	n, err := normalize(t)
	if err != nil {
		return nil, err
	}
	return toPublic(n), nil
}

// Equal 按归一化后的集合相等判定两类型是否相等。
func Equal(a, b *Type) bool {
	na, err := normalize(a)
	if err != nil {
		return false
	}
	nb, err := normalize(b)
	if err != nil {
		return false
	}
	return normKey(na) == normKey(nb)
}
