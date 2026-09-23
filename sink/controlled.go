package sink

import (
	"bytes"
	"sync"
)

// Controlled 是可注入短写、背压、断开与写错误的记录型下游。
// 所有字段在持有自身锁时访问，可被并发调用。
type Controlled struct {
	mu sync.Mutex
	// Buf 保存已接受的全部字节。
	Buf bytes.Buffer
	// MaxPerWrite 限制每次 Write 接受的字节数（0 表示不限）。
	MaxPerWrite int
	// Limit 是累计接受字节上限；达到后断开（0 表示不限）。
	Limit int
	// Backpressured 为真时完全背压（0 字节，ErrBackpressure）。
	Backpressured bool
	// PartialStall 为真时本次至多接受 MaxPerWrite 字节后立即背压。
	PartialStall bool
	// Disconnected 为真时返回 ErrDisconnected。
	Disconnected bool
	// Err 非 nil 时作为硬写错误返回（优先级最高）。
	Err error
	// Calls 统计 Write 调用次数。
	Calls int
}

// Write 实现 Sink。
func (c *Controlled) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Calls++
	if c.Err != nil {
		return 0, c.Err
	}
	if c.Disconnected {
		return 0, ErrDisconnected
	}
	if c.Backpressured {
		return 0, ErrBackpressure
	}
	n := len(p)
	if c.MaxPerWrite > 0 && n > c.MaxPerWrite {
		n = c.MaxPerWrite
	}
	if c.Limit > 0 {
		if c.Buf.Len() >= c.Limit {
			return 0, ErrDisconnected
		}
		if rem := c.Limit - c.Buf.Len(); n > rem {
			n = rem
		}
	}
	if n > 0 {
		c.Buf.Write(p[:n])
	}
	var err error
	if c.PartialStall {
		err = ErrBackpressure
	}
	return n, err
}

// Bytes 返回已接受字节的快照。
func (c *Controlled) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.Buf.Bytes()...)
}

// Len 返回已接受字节数。
func (c *Controlled) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Buf.Len()
}

// SetBackpressure 并发安全地切换背压状态。
func (c *Controlled) SetBackpressure(on bool) {
	c.mu.Lock()
	c.Backpressured, c.PartialStall = on, false
	c.mu.Unlock()
}
