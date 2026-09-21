package upload

// Stat 返回当前账目的快照。
func (u *Upload) Stat() Report {
	u.mu.Lock()
	defer u.mu.Unlock()

	missing := make([]int, len(u.missing))
	copy(missing, u.missing)
	return Report{
		Received: u.received,
		Bytes:    u.bytes,
		Replaced: u.replaced,
		Missing:  missing,
	}
}
