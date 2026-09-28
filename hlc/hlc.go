package hlc

import "fmt"

// Kind 表示事件类型。
type Kind int

const (
	// KindLocal 本地事件。
	KindLocal Kind = iota
	// KindSend 发送事件。
	KindSend
	// KindReceive 接收事件。
	KindReceive
)

// String 返回事件类型的可读名称。
func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindSend:
		return "send"
	case KindReceive:
		return "receive"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// Timestamp 是混合逻辑时钟时间戳：L 为逻辑时间（物理读数与已见逻辑时间的较大者），
// C 为同一逻辑时间上的因果计数。按 (L, C) 字典序比较。
type Timestamp struct {
	L uint64
	C uint64
}

// Compare 按 (L, C) 字典序比较两个时间戳：-1 表示 t 早于 o，0 相等，1 晚于 o。
func (t Timestamp) Compare(o Timestamp) int {
	switch {
	case t.L < o.L:
		return -1
	case t.L > o.L:
		return 1
	case t.C < o.C:
		return -1
	case t.C > o.C:
		return 1
	default:
		return 0
	}
}

// Before 报告 t 是否严格早于 o。
func (t Timestamp) Before(o Timestamp) bool {
	return t.Compare(o) < 0
}

// String 以 "L.C" 形式返回时间戳。
func (t Timestamp) String() string {
	return fmt.Sprintf("%d.%d", t.L, t.C)
}

// ErrorCode 标识一次被拒绝操作的可区分原因。
type ErrorCode int

const (
	// ErrInvalidArgument 非法参数（空节点名、空消息号等）。
	ErrInvalidArgument ErrorCode = iota
	// ErrNodeNotFound 节点不存在。
	ErrNodeNotFound
	// ErrNegativePhysical 物理读数为负。
	ErrNegativePhysical
	// ErrMessageNotFound 消息号不存在。
	ErrMessageNotFound
	// ErrMessageAlreadyReceived 消息已被目标节点接收过。
	ErrMessageAlreadyReceived
	// ErrMessageTargetMismatch 接收方不是消息的目标节点。
	ErrMessageTargetMismatch
	// ErrDuplicateMessageID 消息号与在途或已完成的消息冲突。
	ErrDuplicateMessageID
	// ErrSkewExceeded 消息逻辑时间相对本地物理读数偏差超限。
	ErrSkewExceeded
	// ErrCounterOverflow 因果计数达到 uint64 上限后仍需递增。
	ErrCounterOverflow
)

// String 返回错误码的可读名称。
func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrNodeNotFound:
		return "node not found"
	case ErrNegativePhysical:
		return "negative physical reading"
	case ErrMessageNotFound:
		return "message not found"
	case ErrMessageAlreadyReceived:
		return "message already received"
	case ErrMessageTargetMismatch:
		return "message target mismatch"
	case ErrDuplicateMessageID:
		return "duplicate message id"
	case ErrSkewExceeded:
		return "clock skew exceeded"
	case ErrCounterOverflow:
		return "counter overflow"
	default:
		return fmt.Sprintf("unknown error(%d)", int(c))
	}
}

// Error 是所有被拒绝操作返回的错误类型，携带可机读的错误码与上下文。
type Error struct {
	Code      ErrorCode
	Operation string
	Node      string
	MessageID string
	Detail    string
}

func (e *Error) Error() string {
	s := e.Code.String()
	if e.Operation != "" {
		s += " in " + e.Operation
	}
	switch {
	case e.Node != "" && e.MessageID != "":
		s += fmt.Sprintf(" (node=%q message=%q)", e.Node, e.MessageID)
	case e.Node != "":
		s += fmt.Sprintf(" (node=%q)", e.Node)
	case e.MessageID != "":
		s += fmt.Sprintf(" (message=%q)", e.MessageID)
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

func errf(code ErrorCode, op, node, msgID, format string, args ...any) *Error {
	return &Error{
		Code:      code,
		Operation: op,
		Node:      node,
		MessageID: msgID,
		Detail:    fmt.Sprintf(format, args...),
	}
}

// Event 是事件历史中的一条记录。
type Event struct {
	// Seq 是系统内全局唯一、按发生顺序分配的事件序号。
	Seq uint64
	// Node 是产生该事件的节点。
	Node string
	// Kind 是事件类型。
	Kind Kind
	// Physical 是本次事件给出的非负物理读数。
	Physical uint64
	// Timestamp 是该事件获得的 HLC 时间戳。
	Timestamp Timestamp
	// MessageID 为发送/接收事件携带的消息号，本地事件为空。
	MessageID string
	// To 为发送事件的目标节点，其余事件为空。
	To string
	// From 为接收事件的发送方节点，其余事件为空。
	From string
	// RelatedSeq 指向同一消息另一端事件的全局序号：接收事件指向发送事件。
	RelatedSeq uint64
}
