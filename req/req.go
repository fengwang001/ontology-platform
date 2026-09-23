// Package req 定义写请求与其结果。
package req

import "errors"

// 四类可 errors.Is 区分的错误。
var (
	// ErrClosed 表示组提交器已关闭，不再接收请求。
	ErrClosed = errors.New("req: committer closed")
	// ErrWriteFail 表示批次写入日志失败，整批序号作废。
	ErrWriteFail = errors.New("req: batch write failed")
	// ErrSyncFail 表示批次 fsync 失败，整批序号作废。
	ErrSyncFail = errors.New("req: batch sync failed")
	// ErrTooLarge 保留给上层拒绝场景（本实现不拒绝超大单条，不会返回）。
	ErrTooLarge = errors.New("req: payload too large")
)

// Req 是一条写请求；调用方持有同一指针等待结果。
type Req struct {
	Payload []byte

	Seq int64 // 成功时分配的全局序号（从 1 起）。
	Err error // 失败时的错误。

	done chan struct{}
}

// New 创建一条尚未完成的写请求。
func New(payload []byte) *Req {
	return &Req{Payload: payload, done: make(chan struct{})}
}

// Done 在结果写回后关闭，调用方从中收到唤醒。
func (r *Req) Done() <-chan struct{} { return r.done }

// Finish 由 leader 在落盘结束后调用：写入本条结果并唤醒唯一等待者。
func (r *Req) Finish(seq int64, err error) {
	r.Seq = seq
	r.Err = err
	close(r.done)
}

// Wait 阻塞直到本条请求得到结果。
func (r *Req) Wait() (int64, error) {
	<-r.done
	return r.Seq, r.Err
}
