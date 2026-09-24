// Package req 定义组提交的写请求与结果。
package req

// Request 是一条写请求。每条请求自带独立的结果回传 channel，
// 保证 leader 投递结果时直达本人、不串台。
type Request struct {
	Payload []byte
	// Done 由调用方创建，缓冲必须 >= 1，leader 只投递一次。
	Done chan Result
}

// Result 是一条写请求的最终结果。
// Err 非 nil 时 Seq 无意义；Err == nil 时 Seq 为全局连续序号（从 1 起）。
type Result struct {
	Seq uint64
	Err error
}

// New 创建请求。
func New(payload []byte) Request {
	return Request{Payload: payload, Done: make(chan Result, 1)}
}

// Reply 投递结果，非阻塞（channel 自带缓冲）。
func (r Request) Reply(seq uint64, err error) {
	r.Done <- Result{Seq: seq, Err: err}
}

// Wait 阻塞等待结果。
func (r Request) Wait() Result {
	return <-r.Done
}
