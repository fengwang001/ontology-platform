// Package history 保存每个工作流的追加式事件日志。
//
// 事件只有两种：
//
//   - S(name)：工作流步骤，name 为非空字节串；
//   - M(pid)：补丁标记，pid 为非空字节串。
//
// 日志按工作流（非空字节串）隔离，只允许整体追加，不允许修改或删除。
package history

// Kind 区分事件种类：步骤或补丁标记。
type Kind uint8

const (
	// KindStep 表示 S(name) 事件。
	KindStep Kind = iota + 1
	// KindMarker 表示 M(pid) 事件。
	KindMarker
)

// Event 是日志中的一条事件。Name 在 Kind 为 KindStep 时有效，
// Pid 在 Kind 为 KindMarker 时有效；二者均为非空字节串。
type Event struct {
	Kind Kind
	Name []byte
	Pid  []byte
}

// Step 构造步骤事件 S(name)。调用方需保证 name 非空（Append/Run 会再次校验）。
func Step(name []byte) Event {
	return Event{Kind: KindStep, Name: name}
}

// Marker 构造补丁标记事件 M(pid)。调用方需保证 pid 非空。
func Marker(pid []byte) Event {
	return Event{Kind: KindMarker, Pid: pid}
}

// IsStep 报告事件是否为步骤事件。
func (e Event) IsStep() bool { return e.Kind == KindStep }

// IsMarker 报告事件是否为补丁标记事件。
func (e Event) IsMarker() bool { return e.Kind == KindMarker }

// clone 复制事件内部的字节串，避免调用方持有切片后从外部改动日志。
func (e Event) clone() Event {
	out := Event{Kind: e.Kind}
	if e.Kind == KindStep {
		out.Name = append([]byte(nil), e.Name...)
	} else {
		out.Pid = append([]byte(nil), e.Pid...)
	}
	return out
}
