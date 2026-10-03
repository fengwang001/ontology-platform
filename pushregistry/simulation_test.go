package pushregistry

import "testing"

import "sort"

type simOldToken struct {
	token     string
	retiredAt int64
}

type simBinding struct {
	device   string
	cur      string
	lastSeen int64
	old      []simOldToken
}

type simulator struct {
	d        int
	r        int
	g        int64
	b        int64
	maxNow   int64
	bindings map[string]map[string]*simBinding
	owner    map[string]simOwner
	blocks   map[string]int64
}

type simOwner struct {
	user    string
	device  string
	current bool
}

func TestSkeletonSimulator(t *testing.T) {
	sim := newSimulator(1, 0, 0, 0)
	if sim == nil {
		t.Fatal("nil simulator")
	}
}

func newSimulator(d, r int, g, b int64) *simulator {
	return &simulator{
		d:        d,
		r:        r,
		g:        g,
		b:        b,
		bindings: make(map[string]map[string]*simBinding),
		owner:    make(map[string]simOwner),
		blocks:   make(map[string]int64),
	}
}

func validSimName(value string, maxLength int) bool {
	return value != "" && len(value) <= maxLength
}

func validSimNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func (s *simulator) find(user, device string) *simBinding {
	return s.bindings[user][device]
}

func (s *simulator) release(token string) {
	delete(s.owner, token)
}

func (s *simulator) deleteBinding(user, device string) {
	bound := s.find(user, device)
	s.release(bound.cur)
	for _, item := range bound.old {
		s.release(item.token)
	}
	delete(s.bindings[user], device)
	if len(s.bindings[user]) == 0 {
		delete(s.bindings, user)
	}
}

func (s *simulator) removeOld(user, device, token string) {
	bound := s.find(user, device)
	for i, item := range bound.old {
		if item.token == token {
			bound.old = append(bound.old[:i], bound.old[i+1:]...)
			s.release(token)
			return
		}
	}
}

func (s *simulator) register(user, device, token string, now int64) error {
	if !validSimName(user, 64) || !validSimName(device, 64) ||
		!validSimName(token, 256) || !validSimNow(now) {
		return ErrInvalidArgument
	}
	if now < s.maxNow {
		return ErrClockRollback
	}
	if s.blocks[token] > now {
		return ErrTokenBlocked
	}

	if owner, exists := s.owner[token]; exists {
		if owner.current && owner.user == user && owner.device == device {
			s.find(user, device).lastSeen = now
			s.maxNow = now
			return nil
		}
		if owner.current {
			s.deleteBinding(owner.user, owner.device)
		} else {
			s.removeOld(owner.user, owner.device, token)
		}
	}

	if bound := s.find(user, device); bound != nil {
		previous := bound.cur
		s.release(previous)
		bound.old = append([]simOldToken{{token: previous, retiredAt: now}}, bound.old...)
		if len(bound.old) > s.r {
			for _, item := range bound.old[s.r:] {
				s.release(item.token)
			}
			bound.old = bound.old[:s.r]
		}
		if s.r > 0 {
			s.owner[previous] = simOwner{user: user, device: device, current: false}
		}
		bound.cur = token
		bound.lastSeen = now
		s.owner[token] = simOwner{user: user, device: device, current: true}
	} else {
		if s.bindings[user] == nil {
			s.bindings[user] = make(map[string]*simBinding)
		}
		if len(s.bindings[user]) >= s.d {
			var victimDevice string
			var victimLastSeen int64
			first := true
			for candidate, bound := range s.bindings[user] {
				if first || bound.lastSeen < victimLastSeen ||
					(bound.lastSeen == victimLastSeen && candidate < victimDevice) {
					victimDevice = candidate
					victimLastSeen = bound.lastSeen
					first = false
				}
			}
			s.deleteBinding(user, victimDevice)
			if s.bindings[user] == nil {
				s.bindings[user] = make(map[string]*simBinding)
			}
		}
		s.bindings[user][device] = &simBinding{
			device:   device,
			cur:      token,
			lastSeen: now,
			old:      []simOldToken{},
		}
		s.owner[token] = simOwner{user: user, device: device, current: true}
	}

	s.maxNow = now
	return nil
}

func (s *simulator) feedback(token string, now int64) error {
	if !validSimName(token, 256) || !validSimNow(now) {
		return ErrInvalidArgument
	}
	if now < s.maxNow {
		return ErrClockRollback
	}
	owner, exists := s.owner[token]
	if !exists {
		return ErrTokenNotFound
	}
	if owner.current {
		s.deleteBinding(owner.user, owner.device)
	} else {
		s.removeOld(owner.user, owner.device, token)
	}
	s.blocks[token] = now + s.b
	s.maxNow = now
	return nil
}

func (s *simulator) touch(user, device string, now int64) error {
	if !validSimName(user, 64) || !validSimName(device, 64) || !validSimNow(now) {
		return ErrInvalidArgument
	}
	if now < s.maxNow {
		return ErrClockRollback
	}
	bound := s.find(user, device)
	if bound == nil {
		return ErrDeviceNotFound
	}
	bound.lastSeen = now
	s.maxNow = now
	return nil
}

func (s *simulator) unregister(user, device string, now int64) error {
	if !validSimName(user, 64) || !validSimName(device, 64) || !validSimNow(now) {
		return ErrInvalidArgument
	}
	if now < s.maxNow {
		return ErrClockRollback
	}
	bound := s.find(user, device)
	if bound == nil {
		return ErrDeviceNotFound
	}
	s.deleteBinding(user, device)
	s.maxNow = now
	return nil
}

func (s *simulator) targets(user string, now int64) ([]string, error) {
	if !validSimName(user, 64) || !validSimNow(now) {
		return nil, ErrInvalidArgument
	}
	if now < s.maxNow {
		return nil, ErrClockRollback
	}

	var current []simBinding
	var old []simOldToken
	for _, bound := range s.bindings[user] {
		current = append(current, *bound)
		old = append(old, bound.old...)
	}
	sort.Slice(current, func(i, j int) bool {
		if current[i].lastSeen != current[j].lastSeen {
			return current[i].lastSeen > current[j].lastSeen
		}
		return current[i].device < current[j].device
	})
	sort.Slice(old, func(i, j int) bool {
		if old[i].retiredAt != old[j].retiredAt {
			return old[i].retiredAt > old[j].retiredAt
		}
		return old[i].token < old[j].token
	})

	result := make([]string, 0, len(current)+len(old))
	for i := range current {
		result = append(result, current[i].cur)
	}
	for _, item := range old {
		if now < item.retiredAt+s.g {
			result = append(result, item.token)
		}
	}
	return result, nil
}
