package cep

// Mode 表示先后事件之间的连续性要求。
type Mode int

const (
	// RelaxedContiguity 宽松连续：先事件与后事件之间允许夹杂任意事件，
	// 一个先事件至多配对一个后事件。
	RelaxedContiguity Mode = iota
	// StrictContiguity 严格连续：后事件必须是同一键内紧挨先事件的下一个事件。
	StrictContiguity
)

// String 返回模式的可读名称，用于日志输出。
func (m Mode) String() string {
	switch m {
	case RelaxedContiguity:
		return "relaxed"
	case StrictContiguity:
		return "strict"
	default:
		return "unknown"
	}
}

// Event 是进入匹配组件的一条事件。
// Timestamp 为同一键内单调不减的逻辑时间戳（单位由调用方约定，
// 与 Config.Window 使用同一单位）。
type Event struct {
	Key       string
	Type      string
	Timestamp int64
}

// Match 是一对满足窗口与连续性要求的先后事件配对。
type Match struct {
	Key     string
	First   Event
	Second  Event
	Elapsed int64 // Second.Timestamp - First.Timestamp，闭区间内 <= Window
}

// Config 是匹配器的构造参数。
type Config struct {
	FirstType  string // 先事件类型
	SecondType string // 后事件类型
	Window     int64  // 时间窗口上限（闭区间，与 Event.Timestamp 同单位），必须 > 0
	MaxPending int    // 每个键允许的待匹配先事件队列上限，必须 > 0
	Mode       Mode   // 连续性模式
}
