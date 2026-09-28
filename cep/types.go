package cep

// Event 是进入匹配引擎的单个事件。
// 同一键内时间字段单调不减（相等也合法）。
type Event struct {
	Key      string
	Type     string
	Time     int64
	Sequence int64
}

// Mode 表示连续要求的严格程度。
type Mode int

const (
	// Strict 严格连续：后事件必须与先事件在同键序列中相邻。
	Strict Mode = iota
	// Relaxed 宽松连续：两个事件之间允许夹任意事件。
	Relaxed
)

func (m Mode) valid() bool { return m == Strict || m == Relaxed }

// Config 描述一次匹配任务的参数。
type Config struct {
	// FirstType / SecondType 为先事件与后事件的类型，均不可为空。
	FirstType  string
	SecondType string
	// Window 为后事件时间减去先事件时间的上限（含端点）。必须 >= 0。
	Window int64
	// Mode 为连续要求：Strict 或 Relaxed。
	Mode Mode
	// MaxPending 为每个键允许缓存的待匹配先事件上限；0 表示不限。
	MaxPending int
}

// Pair 是一对成功匹配的事件，先后顺序固定。
type Pair struct {
	First  Event
	Second Event
}
