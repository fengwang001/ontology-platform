package whiteboard

// Lock 对元素或组合加软锁；同持有者再次加锁视为续期（覆盖到期时刻）。
func (b *Board) Lock(user, id string, ttl, now int64) error {
	if !validUser(user) || !validID(id) {
		return reject(KindInvalidArgument, "Lock: user/id must be non-empty")
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return reject(KindInvalidArgument, "Lock: ttl %d out of [%d,%d]", ttl, MinTTL, MaxTTL)
	}
	if !validTime(now) {
		return reject(KindInvalidArgument, "Lock: now out of [0,1e12]")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Lock: now=%d < last=%d", now, b.lastTs)
	}
	if _, isElem := b.elems[id]; !isElem {
		if _, isGroup := b.groups[id]; !isGroup {
			return reject(KindNotFound, "Lock: %q does not exist", id)
		}
	}
	b.purgeExpired(id, now)
	if existing, ok := b.locks[id]; ok && existing.holder != user {
		return &LockError{
			Holder:   existing.holder,
			ExpireAt: existing.expireAt,
			Remain:   existing.expireAt - now,
			TargetID: id,
		}
	}
	b.locks[id] = lockRec{holder: user, expireAt: now + ttl}
	b.lastTs = now
	return nil
}

// Unlock 仅锁持有者可解除；已到期/不存在的锁视为已解锁（无副作用成功）。
func (b *Board) Unlock(user, id string, now int64) error {
	if !validUser(user) || !validID(id) || !validTime(now) {
		return reject(KindInvalidArgument, "Unlock: bad user/id/now")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Unlock: now=%d < last=%d", now, b.lastTs)
	}
	if _, isElem := b.elems[id]; !isElem {
		if _, isGroup := b.groups[id]; !isGroup {
			return reject(KindNotFound, "Unlock: %q does not exist", id)
		}
	}
	b.purgeExpired(id, now)
	existing, ok := b.locks[id]
	if !ok {
		b.lastTs = now
		return nil
	}
	if existing.holder != user {
		return &LockError{
			Holder:   existing.holder,
			ExpireAt: existing.expireAt,
			Remain:   existing.expireAt - now,
			TargetID: id,
		}
	}
	delete(b.locks, id)
	b.lastTs = now
	return nil
}
