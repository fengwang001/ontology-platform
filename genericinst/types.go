package genericinst

// Kind 标识一个规范化后的类型的种类。
type Kind int

const (
	KindNominal  Kind = iota // 已登记的具名类型
	KindStruct               // 结构类型
	KindAlias                // 类型别名（规范化时解析为其目标）
	KindInstance             // 另一个泛型定义的实例
)

// Type 是前端可传入的类型实参表达式的最小接口。
// CanonKey 必须返回一种规范化表示：语义等价的类型其 CanonKey 相同，
// 不等价的类型其 CanonKey 不同。
type Type interface {
	Kind() Kind
	CanonKey() string
}

// Nominal 是已登记的具名类型（如 int、string、Foo）。
type Nominal struct{ Name string }

func NewNominal(name string) *Nominal { return &Nominal{Name: name} }
func (n *Nominal) Kind() Kind         { return KindNominal }
func (n *Nominal) CanonKey() string   { return "N:" + escape(n.Name) }

// Alias 是类型别名，语义上与其目标类型等价（可多层嵌套）。
type Alias struct {
	Name   string
	Target Type
}

func NewAlias(name string, target Type) *Alias { return &Alias{Name: name, Target: target} }
func (a *Alias) Kind() Kind                    { return KindAlias }

// CanonKey 直接透传到目标类型：别名与目标因此永远等价。
func (a *Alias) CanonKey() string {
	return resolveAlias(a).CanonKey()
}

// StructField 是结构类型的一个字段；字段次序是类型身份的一部分。
type StructField struct {
	Name string
	Type Type
}

// Struct 是结构类型，按字段名、字段次序、字段类型逐项比较。
type Struct struct{ Fields []StructField }

func NewStruct(fields []StructField) *Struct { return &Struct{Fields: fields} }
func (s *Struct) Kind() Kind                 { return KindStruct }

func (s *Struct) CanonKey() string {
	b := make([]byte, 0, 32)
	b = append(b, 'S', '[')
	for i, f := range s.Fields {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, escape(f.Name)...)
		b = append(b, '=')
		b = append(b, resolveAlias(f.Type).CanonKey()...)
	}
	b = append(b, ']')
	return string(b)
}

// InstanceRef 把另一个泛型定义的实例用作类型实参。
// 等价实例必然等价（规范化键只依赖定义名与实参键）。
type InstanceRef struct {
	Def  string
	Args []Type
}

func NewInstanceRef(defName string, args []Type) *InstanceRef {
	return &InstanceRef{Def: defName, Args: args}
}

func (ir *InstanceRef) Kind() Kind { return KindInstance }
func (ir *InstanceRef) CanonKey() string {
	b := make([]byte, 0, 32)
	b = append(b, 'I', ':')
	b = append(b, escape(ir.Def)...)
	b = append(b, '(')
	for i, a := range ir.Args {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, resolveAlias(a).CanonKey()...)
	}
	b = append(b, ')')
	return string(b)
}

// resolveAlias 把别名（链）解析为其目标类型，并对循环别名做保护。
func resolveAlias(t Type) Type {
	if t == nil {
		return nil
	}
	seen := map[Type]bool{}
	for t.Kind() == KindAlias {
		if seen[t] {
			break
		}
		seen[t] = true
		a := t.(*Alias)
		if a.Target == nil {
			break
		}
		t = a.Target
	}
	return t
}

// escape 使用 strconv.Quote 生成定长、无分隔符歧义的键片段。
func escape(s string) string {
	return quote(s)
}
