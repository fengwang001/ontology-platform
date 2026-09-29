package hlc

import "errors"

// 各类非法输入对应的可区分错误原因。
var (
	// ErrEmptyNodeID 节点标识为空。
	ErrEmptyNodeID = errors.New("hlc: empty node id")
	// ErrNodeExists 节点已存在。
	ErrNodeExists = errors.New("hlc: node already exists")
	// ErrNodeNotFound 节点不存在。
	ErrNodeNotFound = errors.New("hlc: node not found")
	// ErrNegativePhysical 物理时钟读数为负。
	ErrNegativePhysical = errors.New("hlc: negative physical reading")
	// ErrEmptyMessageID 消息标识为空。
	ErrEmptyMessageID = errors.New("hlc: empty message id")
	// ErrMessageNotFound 消息号不存在。
	ErrMessageNotFound = errors.New("hlc: message not found")
	// ErrMessageAlreadyReceived 消息已被接收。
	ErrMessageAlreadyReceived = errors.New("hlc: message already received")
	// ErrNotRecipient 当前节点不是消息的目标节点。
	ErrNotRecipient = errors.New("hlc: node is not the message recipient")
	// ErrClockOffsetExceeded 物理读数超出允许的最大偏差。
	ErrClockOffsetExceeded = errors.New("hlc: clock offset exceeded")
	// ErrCounterOverflow 逻辑计数达到上限，无法继续推进。
	ErrCounterOverflow = errors.New("hlc: counter overflow")
)
