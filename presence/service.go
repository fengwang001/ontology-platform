package presence

func (s *svc) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	if now < s.now {
		return ErrClockRollback
	}
	return nil
}

func (s *svc) Report(user, device string, status Status, now int64) error {
	if user == "" || device == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	u := s.users[user] // Report 对未知用户仅在被接受时自动创建
	var d *devRec
	if u != nil {
		d = u.devices[device]
	}
	if u != nil && d == nil && len(u.devices) >= maxDevices {
		return ErrTooManyDevices
	}
	if !isDeviceStatus(status) {
		return ErrInvalidStatus
	}

	// 通过全部校验后才允许产生副作用（惰性到期）。
	s.expireDue(now)
	if u == nil {
		u = s.getUser(user)
	}
	if d == nil {
		d = &devRec{}
		u.devices[device] = d
	} else if d.expires > now {
		u.online--
		u.counts[d.status-1]--
	}

	s.leaseSeq++
	d.status = status
	d.expires = now + s.t
	u.online++
	u.counts[status-1]++
	s.addExpiry(user, device, d.expires, s.leaseSeq)

	s.now = now
	s.emit(u, user, now)
	return nil
}

func (s *svc) Offline(user, device string, now int64) error {
	if user == "" || device == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	u := s.users[user]
	if u == nil {
		return ErrUserNotFound
	}
	d := u.devices[device]
	if d == nil {
		return ErrDeviceNotFound
	}

	s.expireDue(now)
	if d.expires > now {
		old := aggregateReal(u)
		u.online--
		u.counts[d.status-1]--
		d.expires = -1
		if cur := aggregateReal(u); old != cur {
			s.emit(u, user, now)
		}
	}
	s.now = now
	return nil
}

func (s *svc) SetInvisible(user string, on bool, now int64) error {
	if user == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	u := s.users[user]
	if u == nil {
		return ErrUserNotFound
	}
	if u.invisible == on {
		return ErrAlreadyInvisible
	}

	s.expireDue(now)
	u.invisible = on
	s.now = now
	s.emit(u, user, now)
	return nil
}

func (s *svc) Block(owner, who string, now int64) error {
	if owner == "" || who == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.users[owner] == nil || s.users[who] == nil {
		return ErrUserNotFound
	}
	if owner == who {
		return ErrBlockSelf
	}
	set := s.blocks[owner]
	if set != nil && set[who] {
		return ErrAlreadyBlocked
	}

	s.expireDue(now)
	if set == nil {
		set = map[string]bool{}
		s.blocks[owner] = set
	}
	set[who] = true
	s.now = now
	// who 作为 owner 的观察者，其可见状态可能变为离线。
	s.emitBlocked(who, owner, now)
	return nil
}

func (s *svc) Unblock(owner, who string, now int64) error {
	if owner == "" || who == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.users[owner] == nil || s.users[who] == nil {
		return ErrUserNotFound
	}
	set := s.blocks[owner]
	if set == nil || !set[who] {
		return ErrNotBlocked
	}

	s.expireDue(now)
	delete(set, who)
	s.now = now
	s.emitOne(who, owner, now)
	return nil
}

func (s *svc) Subscribe(viewer, target string, now int64) error {
	if viewer == "" || target == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	u := s.users[target]
	if s.users[viewer] == nil || u == nil {
		return ErrUserNotFound
	}
	if viewer == target {
		return ErrSubscribeSelf
	}
	if _, ok := u.subscribers[viewer]; ok {
		return ErrAlreadySubscribed
	}

	s.expireDue(now)
	s.now = now
	vis := s.visibleStatus(viewer, target, u)
	u.subscribers[viewer] = vis // 起始可见状态，不产生通知
	if s.viewers[viewer] == nil {
		s.viewers[viewer] = &viewerState{}
	}
	return nil
}

func (s *svc) Query(viewer, target string, now int64) (QueryResult, error) {
	if viewer == "" || target == "" {
		return QueryResult{}, ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return QueryResult{}, err
	}
	u := s.users[target]
	if s.users[viewer] == nil || u == nil {
		return QueryResult{}, ErrUserNotFound
	}

	s.expireDue(now)
	s.now = now
	res := QueryResult{
		Status:      s.visibleStatus(viewer, target, u),
		ActiveCount: u.online,
	}
	if viewer == target {
		res.Devices = deviceList(u)
	}
	return res, nil
}

func (s *svc) Drain(viewer string, now int64) (DrainResult, error) {
	if viewer == "" {
		return DrainResult{}, ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return DrainResult{}, err
	}
	if s.users[viewer] == nil {
		return DrainResult{}, ErrUserNotFound
	}

	s.expireDue(now)
	s.now = now
	v := s.viewers[viewer]
	if v == nil {
		return DrainResult{Notifications: []Notification{}}, nil
	}
	out := DrainResult{Notifications: v.queue, Dropped: v.dropped}
	v.queue = nil
	v.dropped = 0
	return out, nil
}
