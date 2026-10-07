// Package ontology 实现本体平台的对象类型版本迁移、存量实例异步回填，
// 以及迁移期间新旧两种版本结构的读写路由与一致性仲裁。
package ontology

import "errors"

// Version 标识对象类型结构的版本。
type Version int

const (
	VersionOld Version = 1
	VersionNew Version = 2
)

func (v Version) String() string {
	switch v {
	case VersionOld:
		return "old"
	case VersionNew:
		return "new"
	default:
		return "unknown"
	}
}

// AttrName 是属性名。
type AttrName string

// Kind 是单个属性在旧版本与新版本之间的对应关系。
type Kind int

const (
	// KindKeep 保留：属性在新旧版本中同名同义，值原样可见。
	KindKeep Kind = iota
	// KindAdd 新增：旧版本不存在，新版本写入/回填时按 Default 补默认值。
	KindAdd
	// KindDrop 废弃：新版本不再存在，仅旧版本视图可见。
	KindDrop
)

func (k Kind) String() string {
	switch k {
	case KindKeep:
		return "keep"
	case KindAdd:
		return "add"
	case KindDrop:
		return "drop"
	default:
		return "unknown"
	}
}

// Value 是一个属性值。Set 区分"显式存在"与"缺省/不存在"。
type Value struct {
	Set bool
	Val any
}

// Present 构造一个显式存在的属性值。
func Present(v any) Value { return Value{Set: true, Val: v} }

// Props 是一个版本视图下的属性集合。
type Props map[AttrName]Value

// Mapping 声明单个属性在两个版本之间的对应关系。
type Mapping struct {
	Attr    AttrName
	Kind    Kind
	Default Value // 仅 Kind == KindAdd 时有意义
}

// Migration 是一次对象类型版本迁移声明（或对在途迁移的追加/修订）。
type Migration struct {
	ObjectType string
	From       Version
	To         Version
	Mappings   []Mapping
}

var (
	// ErrInvalidArgument 表示参数非法（对应关系自相矛盾、修改已生效的对应关系等）。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrNotFound 表示目标实例不存在（校验次序上晚于参数非法）。
	ErrNotFound = errors.New("ontology: object not found")
)
