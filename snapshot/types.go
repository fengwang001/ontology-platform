package snapshot

// Kind 表示一条记录的种类。
type Kind int

const (
	// KindUnknown 表示缺少或非法的 kind 字段。
	KindUnknown Kind = iota
	// KindObject 表示对象记录。
	KindObject
	// KindLink 表示链接记录。
	KindLink
)

// String 返回种类的文本名称。
func (k Kind) String() string {
	switch k {
	case KindObject:
		return "object"
	case KindLink:
		return "link"
	default:
		return "unknown"
	}
}

// MarshalText 使 Kind 在审计 JSON 中以可读文本输出。
func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// Direction 表示链接记录的方向。
type Direction int

const (
	// DirUnknown 表示缺少或非法的 direction 字段。
	DirUnknown Direction = iota
	// DirForward 表示正向链接。
	DirForward
	// DirReverse 表示反向链接。
	DirReverse
)

// String 返回方向的文本名称。
func (d Direction) String() string {
	switch d {
	case DirForward:
		return "forward"
	case DirReverse:
		return "reverse"
	default:
		return "unknown"
	}
}

// MarshalText 使 Direction 在审计 JSON 中以可读文本输出。
func (d Direction) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Record 是导出快照中的一条原始记录。
//
// 使用指针字段表达“字段缺失”与“字段值为空串”的区别；所有字段均按值
// 持有，校验过程不会原地修改记录内容。
type Record struct {
	Kind      Kind
	ID        *string
	Type      *string
	SourceID  *string
	TargetID  *string
	LinkType  *string
	Direction Direction
}

// Status 是一次校验的总体结论。
type Status int

const (
	// StatusComplete 表示整份记录序列从头到尾自洽。
	StatusComplete Status = iota
	// StatusTruncated 表示在某处发现损坏，结果只信任其前缀。
	StatusTruncated
)

// Reason 表示导致前缀截断的损坏原因分类。
type Reason int

const (
	// ReasonNone 表示未发生截断（序列完整）。
	ReasonNone Reason = iota
	// ReasonObjectCorrupt 表示对象记录自身字段损坏（含重复 ID 类型冲突）。
	ReasonObjectCorrupt
	// ReasonLinkCorrupt 表示链接记录自身字段损坏。
	ReasonLinkCorrupt
	// ReasonLinkReferenceMissing 表示链接记录引用了尚未出现或已损坏的对象。
	ReasonLinkReferenceMissing
)

// Result 是一次校验的结果。
type Result struct {
	// Status 为完整或截断。
	Status Status
	// PrefixLen 是最大可恢复前缀包含的记录数；完整时等于序列全长。
	PrefixLen int
	// BadIndex 是损坏记录的下标（从 0 开始）；完整时为 -1。
	BadIndex int
	// Reason 是截断原因；完整时为 ReasonNone。
	Reason Reason
}
