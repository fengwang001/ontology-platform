package serve

import "errors"

// ErrNotBuilt 表示尚未成功 Build 就尝试写出或查询封装信息。
var ErrNotBuilt = errors.New("serve: assembler has not built a response yet")

// WriteToWriter 是写出接收方的最小抽象（io.Writer 的子集）。
// 实现允许短写：返回 n < len(p) 且 err==nil 时，组装器会从同一断点
// 继续发送剩余字节，直到整段响应体写完。
type WriteToWriter interface {
	Write(p []byte) (n int, err error)
}

// WriteTo 把响应体写出，并在短写时从断点续写。
// 已写出的游标在多次调用之间保留：可以反复调用直到 Done。
// 底层数据错误（取数阶段）只可能在 Build 中出现；这里只处理写出。
func (a *Assembler) WriteTo(w WriteToWriter) (int, error) {
	a.mu.lock()
	defer a.mu.unlock()
	if !a.built {
		return 0, ErrNotBuilt
	}

	written := 0
	for a.written < int64(len(a.body)) {
		n, err := w.Write(a.body[a.written:])
		if n > 0 {
			a.written += int64(n)
			written += n
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, errors.New("serve: writer made no progress")
		}
	}
	return written, nil
}

// Done 报告整段响应体是否已全部写出。
func (a *Assembler) Done() bool {
	a.mu.rlock()
	defer a.mu.runlock()
	return a.built && a.written == int64(len(a.body))
}
