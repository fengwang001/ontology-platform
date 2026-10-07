package ontology

// Principal 是发起权限判定的主体。
type Principal string

// ObjectType 声明一个对象类型及其全部合法属性名。
// 对象类型注册后不可变更，以保证历史快照中的规则判定可复现。
type ObjectType struct {
	Name       string
	Attributes []string
}

func (ot ObjectType) hasAttribute(name string) bool {
	for _, a := range ot.Attributes {
		if a == name {
			return true
		}
	}
	return false
}

// InstanceRef 定位一个对象实例。
type InstanceRef struct {
	Type string
	ID   string
}

func (r InstanceRef) key() string { return r.Type + "/" + r.ID }

// Grant 是授权表中的一条记录：某标签对某主体的可读、可写与可见范围结论。
// 授权与具体实例无关。
//
//	Read    主体是否可读被该标签管辖的属性
//	Write   主体是否可写被该标签管辖的属性
//	Visible 主体是否可见该标签本身（即是否允许得知实例携带该标签）
type Grant struct {
	Read    bool
	Write   bool
	Visible bool
}

// TagRule 声明一个敏感标签的判定规则：
// 以 ObjectType 的若干属性取值（以及可选的其他标签判定结果）为输入，
// 输出实例在当前状态下是否携带该标签。
// 标签携带状态永远按需重新求值，不做跨请求持久化。
type TagRule struct {
	Tag        string
	ObjectType string
	Expr       Expr
}

// Value 是属性取值。系统支持 nil（未设置）、bool、string、int64、float64。
type Value = any

// Result 是一次读取调用的输出。
type Result struct {
	Values  map[string]Value
	Version uint64 // 本次读取所基于的提交版本
}
