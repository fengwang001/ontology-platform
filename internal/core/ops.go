package core

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (r *Room) Join(now, u int64) error {
	if !validNow(now) || u <= 0 {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if r.present(u) {
		r.restore(s)
		return ErrDuplicate
	}
	if len(r.roles) == 0 {
		r.roles[u] = Owner
	} else {
		r.roles[u] = Member
	}
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) Leave(now, u int64) error {
	if !validNow(now) || u <= 0 {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(u) {
		r.restore(s)
		return ErrNotInRoom
	}
	if r.roles[u] == Owner && len(r.roles) > 1 {
		r.restore(s)
		return ErrMustTransfer
	}
	r.leaveMicOrQueue(u)
	delete(r.roles, u)
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) SetRole(now, by, target int64, role int) error {
	if !validNow(now) || by <= 0 || target <= 0 || (role != Admin && role != Member) {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(by) {
		r.restore(s)
		return ErrNotInRoom
	}
	if !r.present(target) {
		r.restore(s)
		return ErrNoTarget
	}
	if !(r.roles[by] > r.roles[target] && r.roles[by] > role) {
		r.restore(s)
		return ErrLowLevel
	}
	r.roles[target] = role
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) Transfer(now, by, target int64) error {
	if !validNow(now) || by <= 0 || target <= 0 || by == target {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(by) {
		r.restore(s)
		return ErrNotInRoom
	}
	if !r.present(target) {
		r.restore(s)
		return ErrNoTarget
	}
	if r.roles[by] != Owner {
		r.restore(s)
		return ErrLowLevel
	}
	r.roles[by] = Admin
	r.roles[target] = Owner
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) Mute(now, by, target, until int64) error {
	if !validNow(now) || by <= 0 || target <= 0 || until <= now {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(by) {
		r.restore(s)
		return ErrNotInRoom
	}
	if !r.present(target) {
		r.restore(s)
		return ErrNoTarget
	}
	if r.roles[by] <= r.roles[target] {
		r.restore(s)
		return ErrLowLevel
	}
	if old, ok := r.mutes[target]; ok && now < old.until && old.level > r.roles[by] {
		r.restore(s)
		return ErrSuppressed
	}
	r.mutes[target] = muteRec{until: until, level: r.roles[by]}
	r.kickOffMic(target)
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) Unmute(now, by, target int64) error {
	if !validNow(now) || by <= 0 || target <= 0 {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(by) {
		r.restore(s)
		return ErrNotInRoom
	}
	if !r.present(target) {
		r.restore(s)
		return ErrNoTarget
	}
	if r.roles[by] <= r.roles[target] {
		r.restore(s)
		return ErrLowLevel
	}
	if old, ok := r.mutes[target]; ok && now < old.until && old.level > r.roles[by] {
		r.restore(s)
		return ErrSuppressed
	}
	if !r.muted(target, now) {
		r.restore(s)
		return ErrNotMuted
	}
	delete(r.mutes, target)
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) TakeMic(now, u int64) error {
	if !validNow(now) || u <= 0 {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(u) {
		r.restore(s)
		return ErrNotInRoom
	}
	if r.muted(u, now) {
		r.restore(s)
		return ErrMuted
	}
	if r.onMic(u) || r.inQueue(u) {
		r.restore(s)
		return ErrDuplicate
	}
	if slot := r.firstFreeSlot(); slot >= 0 {
		r.slots[slot] = u
	} else {
		r.enqueue(u)
	}
	r.fill(now)
	r.maxNow = now
	return nil
}

func (r *Room) DropMic(now, u int64) error {
	if !validNow(now) || u <= 0 {
		return ErrBadArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewind
	}
	r.touched = 0
	s := r.save()
	r.fill(now)
	if !r.present(u) {
		r.restore(s)
		return ErrNotInRoom
	}
	if !r.onMic(u) && !r.inQueue(u) {
		r.restore(s)
		return ErrNotInMic
	}
	r.leaveMicOrQueue(u)
	r.fill(now)
	r.maxNow = now
	return nil
}
