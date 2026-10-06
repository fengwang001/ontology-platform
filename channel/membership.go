package channel

// validateNow 校验时钟：now 必须在 [0,10^12] 且不小于上一次被接受操作的 now。
// 调用方已持有锁。
func (c *Channel) validateNow(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidArg
	}
	if now < c.lastNow {
		return ErrClockRewind
	}
	return nil
}

// Join 使 user 加入频道。首个加入者创建频道并成为管理员；其读水位设为当前
// 最新序号，该时刻之前的历史对其不可见也不计未读。重复加入报参数非法。
func (c *Channel) Join(user string, now int64) error {
	if user == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	if _, exists := c.members[user]; exists {
		return ErrInvalidArg
	}
	isFirst := len(c.members) == 0
	c.members[user] = &member{
		admin:     isFirst,
		watermark: int64(len(c.messages)),
	}
	c.lastNow = now
	return nil
}

// Leave 使成员离开。离开后再 Join 会重新设置读水位（离开重进例外，水位可降）。
func (c *Channel) Leave(user string, now int64) error {
	if user == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	if _, ok := c.members[user]; !ok {
		return ErrNotMember
	}
	delete(c.members, user)
	c.lastNow = now
	return nil
}

// Promote 仅管理员可调用，将 target 提升为管理员。对已是管理员的成员操作幂等成功。
func (c *Channel) Promote(actor, target string, now int64) error {
	if actor == "" || target == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	a, ok := c.members[actor]
	if !ok {
		return ErrNotMember
	}
	if !a.admin {
		return ErrForbidden
	}
	t, ok := c.members[target]
	if !ok {
		return ErrNotFound
	}
	t.admin = true
	c.lastNow = now
	return nil
}

// IsAdmin 报告 user 当前是否为管理员成员（供文档与测试使用）。
func (c *Channel) IsAdmin(user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[user]
	return ok && m.admin
}

// Watermark 返回 user 当前的读水位；非成员返回错误（供测试使用）。
func (c *Channel) Watermark(user string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	return m.watermark, nil
}
