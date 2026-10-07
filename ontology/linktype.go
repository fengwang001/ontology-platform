package ontology

import "strconv"

type Direction int

const (
	// Forward 表示链接类型声明时的 source -> target 方向。
	Forward Direction = iota
	// Backward 表示 target -> source 的反向。
	Backward
)

// String 返回方向名，便于审计记录阅读。
func (d Direction) String() string {
	switch d {
	case Forward:
		return "forward"
	case Backward:
		return "backward"
	default:
		return "unknown"
	}
}

// Cardinality 是一个方向上的基数上限。unlimited 为真时不限数量；
// 否则 limit 是该方向每个尾实例已登记链接的硬上限（0 表示禁止任何链接）。
type Cardinality struct {
	unlimited bool
	limit     uint64
}

// LinkType 声明两个对象类型之间的一种链接，两个方向可分别给出不同上限。
type LinkType struct {
	id           string
	sourceType   string
	targetType   string
	forwardCap   Cardinality
	backwardCap  Cardinality
	discrimAttrs []string // 有序：决定区分属性组合的序列化方式
	discrimSet   map[string]struct{}
}

// NewLinkType 注册一个链接类型声明。
func NewLinkType(id string, sourceType, targetType string, forward, backward Cardinality, discrimAttrs []string) *LinkType {
	discrimSet := make(map[string]struct{}, len(discrimAttrs))
	for _, attr := range discrimAttrs {
		discrimSet[attr] = struct{}{}
	}
	return &LinkType{
		id:           id,
		sourceType:   sourceType,
		targetType:   targetType,
		forwardCap:   forward,
		backwardCap:  backward,
		discrimAttrs: append([]string(nil), discrimAttrs...),
		discrimSet:   discrimSet,
	}
}

// Unlimited 构造“不限”基数。
func Unlimited() Cardinality { return Cardinality{unlimited: true} }

// AtMost 构造有限基数上限。AtMost(0) 表示该方向一条链接都不允许。
func AtMost(limit uint64) Cardinality { return Cardinality{limit: limit} }

// ID 返回链接类型标识。
func (lt *LinkType) ID() string { return lt.id }

// SourceType / TargetType 返回声明方向两端的对象类型。
func (lt *LinkType) SourceType() string { return lt.sourceType }
func (lt *LinkType) TargetType() string { return lt.targetType }

// DiscriminatorAttrs 返回区分属性名的有序副本。
func (lt *LinkType) DiscriminatorAttrs() []string {
	return append([]string(nil), lt.discrimAttrs...)
}

// Allows 判断该链接类型是否允许在 tailType/headType 两个对象类型之间
// 按方向 d 建立链接。
func (lt *LinkType) Allows(d Direction, tailType, headType string) bool {
	switch d {
	case Forward:
		return tailType == lt.sourceType && headType == lt.targetType
	case Backward:
		return tailType == lt.targetType && headType == lt.sourceType
	default:
		return false
	}
}

// Cap 返回指定方向声明的基数上限。
func (lt *LinkType) Cap(d Direction) Cardinality {
	if d == Backward {
		return lt.backwardCap
	}
	return lt.forwardCap
}

// Unlimited 报告是否不限数量。
func (c *Cardinality) Unlimited() bool { return c.unlimited }

// Limit 返回有限上限；第二个返回值为 false 表示不限。
func (c *Cardinality) Limit() (uint64, bool) {
	if c.unlimited {
		return 0, false
	}
	return c.limit, true
}

// String 以“*”或数字呈现上限，供审计使用。
func (c *Cardinality) String() string {
	if c.unlimited {
		return "*"
	}
	return strconv.FormatUint(c.limit, 10)
}
