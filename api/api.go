// Package api 是有界乱序重放缓冲器的并发安全门面：所有读写经同一把互斥锁串行化。
// 依赖方向单向：api → reorder → seq。
package api

import (
	"sync"

	"ontology/reorder"
	"ontology/seq"
)

// Buffer 对外暴露缓冲器；内部 reorder.Buffer 的字段全部非导出。
type Buffer struct {
	mu sync.Mutex
	b  *reorder.Buffer
}

// New 创建缓冲器；maxBuffered<1 或 timeout<0 返回 reorder.ErrBadParams，无对象产生。
func New(maxBuffered, timeout int) (*Buffer, error) {
	rb, err := reorder.New(maxBuffered, timeout)
	if err != nil {
		return nil, err
	}
	return &Buffer{b: rb}, nil
}

// Feed 投递一条事件，返回本次（命中及其级联）发出的事件。
func (x *Buffer) Feed(ev seq.Event) ([]seq.Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.b.Feed(ev)
}

// Tick 推进逻辑时钟，返回本次超时 flush（含级联）发出的事件。
func (x *Buffer) Tick() ([]seq.Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.b.Tick()
}

// View 返回已发出事件的拷贝；与 Feed/Tick 及其他读者互斥。
func (x *Buffer) View() []seq.Event {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.b.View()
}

// Lost 返回丢失 Seq 的升序拷贝；与 Feed/Tick 及其他读者互斥。
func (x *Buffer) Lost() []int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.b.Lost()
}

// SelfCheck 委托给 reorder.SelfCheck：后者只在局部实例上核验，不触碰接收者状态，
// 因此无需持锁，可与其他 SelfCheck / View / Lost 并发调用。
func (x *Buffer) SelfCheck() error { return reorder.SelfCheck() }
