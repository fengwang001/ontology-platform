// Package snapshot 提供本体子图快照记录序列的完整性校验与损坏定位。
package snapshot

// Direction 表示链接记录的方向。
type Direction string

const (
	DirectionForward Direction = "forward"
	DirectionReverse Direction = "reverse"
)

// valid 报告方向取值是否合法。
func (d Direction) valid() bool {
	return d == DirectionForward || d == DirectionReverse
}

// Record 是快照记录序列中的一条记录，只允许 ObjectRecord 与 LinkRecord。
type Record interface {
	isRecord()
}

// ObjectRecord 声明一个对象标识与其对象类型。
// 空字符串表示字段缺失。
type ObjectRecord struct {
	ObjectID   string
	ObjectType string
}

// LinkRecord 声明两个对象标识、链接类型与方向。
// 空字符串表示字段缺失。
type LinkRecord struct {
	SourceID  string
	TargetID  string
	LinkType  string
	Direction Direction
}

func (ObjectRecord) isRecord() {}
func (LinkRecord) isRecord()   {}

// Corruption 表示导致最大可恢复前缀截断的损坏原因分类。
type Corruption int

const (
	// CorruptionNone 表示不存在损坏（序列为空或完整自洽）。
	CorruptionNone Corruption = iota
	// CorruptionObjectFields 表示对象记录自身字段损坏
	// （字段缺失，或同一对象标识重复声明了不同的对象类型）。
	CorruptionObjectFields
	// CorruptionLinkFields 表示链接记录自身字段损坏
	// （字段缺失或方向取值不合法）。
	CorruptionLinkFields
	// CorruptionMissingReference 表示链接记录引用了尚未出现
	// 或已被判定为自身损坏的对象记录。
	CorruptionMissingReference
)

// String 返回损坏原因的可读描述。
func (c Corruption) String() string {
	switch c {
	case CorruptionNone:
		return "none"
	case CorruptionObjectFields:
		return "object-record-corrupt"
	case CorruptionLinkFields:
		return "link-record-fields-corrupt"
	case CorruptionMissingReference:
		return "link-record-missing-reference"
	default:
		return "unknown"
	}
}

// Result 是一次校验的结果。
//
// Complete 为 true 表示整份记录序列从头到尾自洽（包括空序列），
// 此时 Corruption 为 CorruptionNone 且 BadIndex 为 -1。
// Complete 为 false 表示在 BadIndex 处发现损坏，PrefixLen 为
// 该条记录之前所有记录构成的最大可恢复前缀长度（可能为 0）。
type Result struct {
	Complete   bool
	PrefixLen  int
	Corruption Corruption
	BadIndex   int

	// recordsRead 是内部度量：校验过程中实际读取过的记录数目。
	// 它不对调用者暴露，仅供包内测试验证读取量不超过 PrefixLen+1。
	recordsRead int
}

// Validate 逐条扫描记录序列，返回最大可恢复前缀与损坏原因。
//
// 校验不修改输入，不持有任何调用间共享状态，可并发调用且结果确定。
// 时间复杂度为 O(len(records))，实际读取的记录数不超过前缀长度加一。
func Validate(records []Record) Result {
	// declared 记录已纳入前缀的对象标识及其对象类型。
	// 只有字段完好且与先前声明一致的对象记录才会进入该表，
	// 因此链接记录查表命中即表示引用满足先出现约束。
	declared := make(map[string]string, len(records))

	for i, rec := range records {
		switch r := rec.(type) {
		case ObjectRecord:
			if r.ObjectID == "" || r.ObjectType == "" {
				return corruptResult(i, CorruptionObjectFields)
			}
			if prev, ok := declared[r.ObjectID]; ok && prev != r.ObjectType {
				return corruptResult(i, CorruptionObjectFields)
			}
			declared[r.ObjectID] = r.ObjectType
		case LinkRecord:
			if r.SourceID == "" || r.TargetID == "" || r.LinkType == "" || !r.Direction.valid() {
				return corruptResult(i, CorruptionLinkFields)
			}
			if _, ok := declared[r.SourceID]; !ok {
				return corruptResult(i, CorruptionMissingReference)
			}
			if _, ok := declared[r.TargetID]; !ok {
				return corruptResult(i, CorruptionMissingReference)
			}
		default:
			// 记录类型无法识别（如 nil），视为对象记录自身字段损坏。
			return corruptResult(i, CorruptionObjectFields)
		}
	}

	return Result{
		Complete:    true,
		PrefixLen:   len(records),
		Corruption:  CorruptionNone,
		BadIndex:    -1,
		recordsRead: len(records),
	}
}

// corruptResult 构造在 index 处截断的校验结果。
func corruptResult(index int, c Corruption) Result {
	return Result{
		Complete:    false,
		PrefixLen:   index,
		Corruption:  c,
		BadIndex:    index,
		recordsRead: index + 1,
	}
}
