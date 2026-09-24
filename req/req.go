// Package req 定义组提交管线使用的写请求与其结果。
package req

// Request 是一条写入请求。Payload 允许为空字节串。
type Request struct {
	Payload []byte
}

// Result 是一条写入请求落盘后得到的结果。
// Seq 为该条在全局日志中的序号，从 0 起连续分配；Err 非空时 Seq 无效（为 0）。
type Result struct {
	Seq int64
	Err error
}

// Pending 把请求与其专属结果通道绑定，保证唤醒结果不串台。
type Pending struct {
	Req Request
	Done chan Result
}

// NewPending 为一条请求创建等待者，Done 带 1 个缓冲使 leader 发送永不阻塞。
func NewPending(r Request) Pending {
	return Pending{Req: r, Done: make(chan Result, 1)}
}
