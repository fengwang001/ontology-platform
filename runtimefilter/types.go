package runtimefilter

import "time"

// JoinType 标识哈希连接的连接类型。
type JoinType int

const (
	JoinUnknown JoinType = iota
	JoinInner
	JoinLeftOuter
	JoinRightOuter
	JoinFullOuter
	JoinSemi
	JoinAnti
)

func (j JoinType) String() string {
	switch j {
	case JoinInner:
		return "inner"
	case JoinLeftOuter:
		return "left-outer"
	case JoinRightOuter:
		return "right-outer"
	case JoinFullOuter:
		return "full-outer"
	case JoinSemi:
		return "semi"
	case JoinAnti:
		return "anti"
	default:
		return "unknown"
	}
}

// Row 是探测侧一行的抽象。
type Row any

// KeyOf 从一行中提取连接键；第二个返回值为 false 表示该行为空键。
type KeyOf[K comparable] func(row Row) (K, bool)

// ShardReport 是一个构建侧分片的键取值摘要。
type ShardReport[K comparable] struct {
	Shard    int
	Min      K
	Max      K
	Distinct map[K]struct{}
	// Empty 表示该分片没有任何构建键，Min/Max 无意义。
	Empty   bool
	Abandon bool
}

// Config 描述一次过滤协调器的静态配置。
type Config[K comparable] struct {
	JoinType    JoinType
	ShardCount  int
	MaxDistinct int
	ReadyWait   time.Duration
	Logger      Logger
	Now         func() time.Time
	After       func(time.Duration) <-chan time.Time
}

// filter 是一个不可变的、已就绪的过滤器版本快照。
type filter[K orderedKey] struct {
	version    int
	min        K
	max        K
	distinct   map[K]struct{} // nil 表示已降级为仅最小最大值
	buildEmpty bool
}
