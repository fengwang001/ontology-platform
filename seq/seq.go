// Package seq 定义生产者/消费者类型别名与哨兵错误。
package seq

import "ontology/ring"

// 哨兵错误直接复用 ring 的定义，保证 errors.Is 可区分。
var (
	ErrBadCap = ring.ErrBadCap
	ErrFull   = ring.ErrFull
)

// Producer 是写入侧视角的缓冲区别名。
type Producer[T any] = ring.Buffer[T]

// Consumer 是读取侧视角的缓冲区别名。
type Consumer[T any] = ring.Buffer[T]

// NewPair 创建一对共享同一缓冲的生产者与消费者。
func NewPair[T any](cap int) (*Producer[T], *Consumer[T], error) {
	b, err := ring.New[T](cap)
	if err != nil {
		return nil, nil, err
	}
	return b, b, nil
}
