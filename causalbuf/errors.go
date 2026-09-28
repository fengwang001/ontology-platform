// Package causalbuf 实现基于向量时钟的因果交付缓冲。
//
// 乱序或重复到达的广播消息先在这里判定：满足因果条件的立即交付，
// 暂不满足的进入有界缓冲等待其因果前驱，重复消息被丢弃并计数。
package causalbuf

import "errors"

// 可区分的拒绝原因。被拒绝的 Receive 调用不会改变任何内部状态。
var (
	// ErrInvalidArgument 参数非法（如空消息、容量为 0）。
	ErrInvalidArgument = errors.New("causalbuf: invalid argument")
	// ErrSenderOutOfRange 发送方编号越界。
	ErrSenderOutOfRange = errors.New("causalbuf: sender out of range")
	// ErrInvalidVector 向量非法（长度不符、含负值等）。
	ErrInvalidVector = errors.New("causalbuf: invalid vector")
	// ErrBufferFull 缓冲已满，无法暂存尚不能交付的消息。
	ErrBufferFull = errors.New("causalbuf: buffer full")
)
