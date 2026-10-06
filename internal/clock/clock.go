package clock

import "sync"

// Clock 是全局单调时钟，记录最后一次被接受操作的时刻。
type Clock struct {
	mu  sync.Mutex
	now int64
}

func New() *Clock {
	return &Clock{}
}

// Now 返回最后一次被接受操作的时刻。
func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 校验时刻不小于当前时刻；合法则推进并返回 ok=true。
// 被拒绝（时钟回退）时不改变时钟。
func (c *Clock) Advance(t int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < c.now {
		return false
	}
	c.now = t
	return true
}

// Peek 是 Now 的只读别名，供提交协议在加业务锁后再次确认时钟。
func (c *Clock) Peek() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
