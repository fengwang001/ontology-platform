package req

import "errors"

// ErrClosed 在组提交通道关闭后提交时返回。
var ErrClosed = errors.New("req: committer closed")

// Request 是一条写请求。调用方阻塞等待 Done 上的 Result。
type Request struct {
	Payload []byte
	Done    chan Result
}

// Result 是一条写请求的结果。Err 非空表示该批落盘失败。
type Result struct {
	Seq uint64
	Err error
}

// New 构造一条请求，Done 带 1 缓冲，leader 发送结果不被阻塞。
func New(payload []byte) *Request {
	return &Request{Payload: payload, Done: make(chan Result, 1)}
}

// Wait 阻塞直到该请求的结果到达。
func (r *Request) Wait() Result { return <-r.Done }

// Complete 把结果发回调用方；缓冲为 1，发送不阻塞且只会成功一次。
func (r *Request) Complete(res Result) { r.Done <- res }
