// Package ontology 实现分布式逻辑时钟（Lamport Clock）与事件全序/因果判定组件。
//
// 每个节点维护一个单调递增的 Lamport 逻辑时钟：
//   - 本地事件、发送事件：时钟加一；
//   - 接收事件：取消息携带时间戳与本地时钟的较大者再加一。
//
// 全序按 (时间戳, 节点编号) 排序，与事件实际执行/到达的先后无关；
// 因果先后（happens-before）由同节点先后、发送→接收边及其传递闭包构成。
package ontology

import "strconv"

// EventType 是事件类型：本地、发送、接收。
type EventType int

const (
	// Local 本地事件，仅推进本节点时钟。
	Local EventType = iota
	// Send 发送事件，推进时钟并登记所发送的消息。
	Send
	// Receive 接收事件，按消息携带的时间戳与本地时钟的较大者推进。
	Receive
)

// String 返回事件类型的可读名称。
func (t EventType) String() string {
	switch t {
	case Local:
		return "local"
	case Send:
		return "send"
	case Receive:
		return "receive"
	default:
		return "unknown"
	}
}

// ErrorKind 标识被拒绝操作的具体原因，各原因彼此可区分。
type ErrorKind int

const (
	// ErrNodeOutOfRange 节点编号越界（小于 0 或大于等于节点总数）。
	ErrNodeOutOfRange ErrorKind = iota
	// ErrMessageNotFound 接收了一条从未被发送（登记）过的消息。
	ErrMessageNotFound
	// ErrDuplicateReceive 同一条消息被重复接收。
	ErrDuplicateReceive
	// ErrTooManyEvents 已接受事件总数达到上限。
	ErrTooManyEvents
	// ErrDuplicateSend 同一消息 ID 被重复发送（登记）。
	ErrDuplicateSend
	// ErrInvalidMessageID 消息 ID 为空。
	ErrInvalidMessageID
	// ErrInvalidNodeCount 构造系统时节点数不合法（<=0）。
	ErrInvalidNodeCount
	// ErrInvalidEventLimit 构造系统时事件上限不合法（<=0）。
	ErrInvalidEventLimit
	// ErrUnknownEvent 因果/排序查询引用了不存在的事件。
	ErrUnknownEvent
)

// String 返回错误原因的稳定机器可读名称。
func (k ErrorKind) String() string {
	switch k {
	case ErrNodeOutOfRange:
		return "node_out_of_range"
	case ErrMessageNotFound:
		return "message_not_found"
	case ErrDuplicateReceive:
		return "duplicate_receive"
	case ErrTooManyEvents:
		return "too_many_events"
	case ErrDuplicateSend:
		return "duplicate_send"
	case ErrInvalidMessageID:
		return "invalid_message_id"
	case ErrInvalidNodeCount:
		return "invalid_node_count"
	case ErrInvalidEventLimit:
		return "invalid_event_limit"
	case ErrUnknownEvent:
		return "unknown_event"
	default:
		return "unknown_error"
	}
}

// ClockError 描述一次被拒绝的操作及其原因。
// 被拒绝的操作不会改变任何节点时钟、事件编号、全序或消息登记。
type ClockError struct {
	// Kind 是可区分的拒绝原因。
	Kind ErrorKind
	// Op 是被拒绝的操作名称（local/send/receive/...）。
	Op string
	// Node 是操作涉及的节点（不适用时为 -1）。
	Node int
	// MessageID 是操作涉及的消息（不适用时为空）。
	MessageID string
	// Detail 补充人类可读细节。
	Detail string
}

// Error 实现 error 接口。
func (e *ClockError) Error() string {
	msg := e.Kind.String()
	if e.Op != "" {
		msg += ": " + e.Op
	}
	if e.Node >= 0 {
		msg += " node=" + strconv.Itoa(e.Node)
	}
	if e.MessageID != "" {
		msg += " message=" + e.MessageID
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// Event 是一次被接受事件的只读快照。
type Event struct {
	// Node 是产生该事件的节点编号。
	Node int
	// Seq 是该节点上 1 起始、连续不间断的事件编号。
	Seq int
	// Kind 是事件类型。
	Kind EventType
	// Clock 是该事件的 Lamport 逻辑时间戳。
	Clock int
	// MessageID 是发送/接收事件关联的消息 ID，本地事件为空。
	MessageID string
	// Vector 是该事件的向量时钟副本，用于因果判定。
	Vector []int
}

// EventRef 以 (节点, 节点内序号) 唯一引用一个事件，与执行先后无关。
type EventRef struct {
	Node int
	Seq  int
}
