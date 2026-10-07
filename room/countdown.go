package room

// countdown 为就绪倒计时。到期时刻 = start + C，恰等于视为已到期。
type countdown struct {
	active bool
	start  int64
	expiry int64
}

func (c *countdown) begin(start, duration int64) {
	c.active = true
	c.start = start
	c.expiry = start + duration
}

func (c *countdown) clear() { *c = countdown{} }

// expired 判断在时刻 now 是否已到期（恰等于视为已到期）。
func (c *countdown) expired(now int64) bool { return c.active && now >= c.expiry }
