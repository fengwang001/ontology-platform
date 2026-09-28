// Package cep 提供同键事件流上的复杂事件匹配：
// 在时间窗口内寻找“先事件 -> 后事件”的配对，
// 支持严格连续与宽松连续两种模式。
package cep

// Contiguity 表示先后事件之间的连续性要求。
type Contiguity int

const (
	// Strict 要求后事件在同键事件流中紧挨着先事件。
	Strict Contiguity = iota
	// Relaxed 允许先后事件之间夹任意事件。
	Relaxed
)

// String 返回连续性模式的可读名称。
func (c Contiguity) String() string {
	switch c {
	case Strict:
		return "strict"
	case Relaxed:
		return "relaxed"
	default:
		return "unknown"
	}
}

// Event 是事件流中的一条事件。同一键的 Timestamp 必须单调不减。
type Event struct {
	Key       string
	Type      string
	Timestamp int64
}

// Match 是一次成功配对的结果。
type Match struct {
	Key    string
	First  Event
	Second Event
	// Delta 为 Second.Timestamp - First.Timestamp，满足 0 <= Delta <= MaxWindow。
	Delta int64
}

// Config 是匹配器的配置。
type Config struct {
	// FirstType 为先事件类型，SecondType 为后事件类型，均不能为空。
	FirstType  string
	SecondType string
	// MaxWindow 为时间窗口上限（闭区间），必须为正数。
	MaxWindow int64
	// MaxPending 为单键待匹配先事件队列上限，必须为正数。
	MaxPending int
	// Contiguity 为连续性模式。
	Contiguity Contiguity
}
