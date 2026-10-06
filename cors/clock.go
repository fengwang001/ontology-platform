package cors

// Clock 是内核使用的手动时钟，只能单调前进。
type Clock struct {
	now int64
}

// Now 返回当前时刻。
func (c *Clock) Now() int64 { return c.now }
