package consignment

// clock 是单调时钟：只接受不小于上一次被接受操作时刻的时刻。
// 被拒绝的操作不得推进时钟。查询只读，不推进时钟。
type clock struct {
	last int64 // 上一次被接受操作的时刻，初始为 0（时刻为非负整数秒）
}

// check 校验时刻合法性：t 为负属参数非法；t < last 属时钟回退。
func (c *clock) check(t int64) error {
	if t < 0 {
		return ErrInvalidParam
	}
	if t < c.last {
		return ErrClockRewind
	}
	return nil
}

// accept 在接受操作后推进时钟。调用前必须先通过 check。
func (c *clock) accept(t int64) {
	c.last = t
}

// now 返回当前时钟，只读。
func (c *clock) now() int64 {
	return c.last
}
