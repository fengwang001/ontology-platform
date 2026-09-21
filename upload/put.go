package upload

// Put 登记第 n 片（从 1 起）。同一 n 再次 Put 视为覆盖：
// Received 不变，Bytes 减去旧值再加新值，Replaced 加一。
func (u *Upload) Put(n int, size int64, etag string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.completed {
		return ErrCompleted
	}
	if n < 1 || n > u.total {
		return ErrBadPart
	}
	if size <= 0 {
		return ErrBadPart
	}
	if n != u.total && size < u.minPart {
		return ErrBadPart
	}

	if old, ok := u.parts[n]; ok {
		u.bytes -= old.size
		u.replaced++
	} else {
		u.received++
	}
	u.parts[n] = part{size: size, etag: etag}
	u.bytes += size
	return nil
}
