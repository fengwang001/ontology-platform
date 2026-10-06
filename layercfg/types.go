package layercfg

// Layer 表示配置层级，数值越小作用范围越宽。
type Layer int

const (
	LayerGlobal Layer = iota
	LayerEnv
	LayerRegion
	LayerInstance
)

func (l Layer) String() string {
	switch l {
	case LayerGlobal:
		return "global"
	case LayerEnv:
		return "env"
	case LayerRegion:
		return "region"
	case LayerInstance:
		return "instance"
	default:
		return "unknown"
	}
}

// Ref 定位某一层：全局层的 Env/Region/Instance 均为空。
type Ref struct {
	Layer    Layer
	Env      string
	Region   string
	Instance string
}

// Scope 是一次解析的三元组；Region/Instance 为空表示只解析到相应层。
type Scope struct {
	Env      string
	Region   string
	Instance string
}

// ValueType 为键允许的标量/列表类型。
type ValueType int

const (
	TypeString ValueType = iota + 1
	TypeInt
	TypeBool
	TypeStringList
)

// MergeMode 为键在多层叠加时的合并方式。
type MergeMode int

const (
	// MergeReplace 覆盖：窄层值整体取代宽层值（标量唯一合法方式）。
	MergeReplace MergeMode = iota + 1
	// MergeAppend 追加：仅字符串列表允许，按从宽到窄拼接并首次出现去重。
	MergeAppend
)

// Schema 是键的模式登记，始终以最新登记解释（不受版本影响）。
type Schema struct {
	Key      string
	Type     ValueType
	Required bool
	Min      int64
	Max      int64
	hasRange bool
	Merge    MergeMode
}

// Value 是类型化配置值。Type==0 表示零值。
type Value struct {
	Type ValueType
	Str  string
	Int  int64
	Bool bool
	List []string
}

// Result 是单键解析结果；Present=false 表示未设置。
type Result struct {
	Present bool
	Value   Value
}

// WriteKind 区分某层对某键槽位的两种写入。
type WriteKind int

const (
	WriteValue WriteKind = iota + 1
	WriteCancel
)

// Entry 是某层某键的值槽内容（不可变）。
type Entry struct {
	Kind  WriteKind
	Value Value
}

// ChangeOp 为一次发布中单条变更的操作种类。
type ChangeOp int

const (
	OpSetValue ChangeOp = iota + 1
	OpCancel
	OpClearWrite
	OpLock
	OpUnlock
)

// Change 是对某层某键的一条发布变更。
type Change struct {
	Op    ChangeOp
	Ref   Ref
	Key   string
	Value Value
}
