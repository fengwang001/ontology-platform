package quota

// naiveSim is a from-scratch reference implementation written directly
// from the specification. It deliberately shares no state or helper code
// with Manager so the random differential test compares two independent
// readings of the rules.
type naiveEnt struct {
	soft  int64
	hard  int64
	usage int64
	grace int64 // -1 unset
}

type naiveUser struct {
	group int
	ent   naiveEnt
}

type naiveSim struct {
	gu, gg int64
	users  map[int]*naiveUser
	groups map[int]*naiveEnt
	last   int64
}

func newNaive(gu, gg int64) *naiveSim {
	return &naiveSim{
		gu: gu, gg: gg,
		users:  map[int]*naiveUser{},
		groups: map[int]*naiveEnt{},
		last:   -1,
	}
}

func nValidID(id int) bool     { return 0 <= id && id <= 1_000_000 }
func nValidNow(now int64) bool { return 0 <= now && now <= 1_000_000_000_000_000 }

func nValidLimits(soft, hard int64) bool {
	return 0 <= soft && soft <= hard && hard <= 1_000_000_000_000_000
}

func nGrace(s *naiveSim, e *naiveEnt, kind Kind, now int64) bool {
	if e.grace == -1 {
		return false
	}
	window := s.gu
	if kind == KindGroup {
		window = s.gg
	}
	return now >= e.grace+window
}

func nTidy(e *naiveEnt, now int64) {
	if e.usage <= e.soft {
		e.grace = -1
	} else if e.grace == -1 {
		e.grace = now
	}
}

func (s *naiveSim) addGroup(g int, soft, hard int64) RejectReason {
	if !nValidID(g) || !nValidLimits(soft, hard) {
		return ReasonInvalidArgument
	}
	if _, exists := s.groups[g]; exists {
		return ReasonIllegalState
	}
	s.groups[g] = &naiveEnt{soft: soft, hard: hard, grace: -1}
	return -1
}

func (s *naiveSim) addUser(u, g int, soft, hard int64) RejectReason {
	if !nValidID(u) || !nValidID(g) || !nValidLimits(soft, hard) {
		return ReasonInvalidArgument
	}
	if _, exists := s.users[u]; exists {
		return ReasonIllegalState
	}
	if _, exists := s.groups[g]; !exists {
		return ReasonNotFound
	}
	s.users[u] = &naiveUser{
		group: g,
		ent:   naiveEnt{soft: soft, hard: hard, grace: -1},
	}
	return -1
}

func (s *naiveSim) alloc(u int, x, now int64) RejectReason {
	if !nValidID(u) || x < 1 || x > 1_000_000_000_000 || !nValidNow(now) {
		return ReasonInvalidArgument
	}
	if now < s.last {
		return ReasonClockRollback
	}
	user, ok := s.users[u]
	if !ok {
		return ReasonNotFound
	}
	group := s.groups[user.group]
	if user.ent.usage+x > user.ent.hard {
		return ReasonUserHardLimit
	}
	if nGrace(s, &user.ent, KindUser, now) {
		return ReasonUserGraceExpired
	}
	if group.usage+x > group.hard {
		return ReasonGroupHardLimit
	}
	if nGrace(s, group, KindGroup, now) {
		return ReasonGroupGraceExpired
	}
	user.ent.usage += x
	group.usage += x
	nTidy(&user.ent, now)
	nTidy(group, now)
	s.last = now
	return -1
}

func (s *naiveSim) free(u int, x, now int64) RejectReason {
	if !nValidID(u) || x < 1 || x > 1_000_000_000_000 || !nValidNow(now) {
		return ReasonInvalidArgument
	}
	if now < s.last {
		return ReasonClockRollback
	}
	user, ok := s.users[u]
	if !ok {
		return ReasonNotFound
	}
	if x > user.ent.usage {
		return ReasonIllegalState
	}
	group := s.groups[user.group]
	user.ent.usage -= x
	group.usage -= x
	nTidy(&user.ent, now)
	nTidy(group, now)
	s.last = now
	return -1
}

func (s *naiveSim) setLimits(kind Kind, id int, soft, hard, now int64) RejectReason {
	if (kind != KindUser && kind != KindGroup) || !nValidID(id) ||
		!nValidLimits(soft, hard) || !nValidNow(now) {
		return ReasonInvalidArgument
	}
	if now < s.last {
		return ReasonClockRollback
	}
	var e *naiveEnt
	if kind == KindUser {
		u, ok := s.users[id]
		if !ok {
			return ReasonNotFound
		}
		e = &u.ent
	} else {
		g, ok := s.groups[id]
		if !ok {
			return ReasonNotFound
		}
		e = g
	}
	e.soft = soft
	e.hard = hard
	nTidy(e, now)
	s.last = now
	return -1
}

func (s *naiveSim) move(u, g2 int, now int64) RejectReason {
	if !nValidID(u) || !nValidID(g2) || !nValidNow(now) {
		return ReasonInvalidArgument
	}
	if now < s.last {
		return ReasonClockRollback
	}
	user, ok := s.users[u]
	if !ok {
		return ReasonNotFound
	}
	target, ok := s.groups[g2]
	if !ok {
		return ReasonNotFound
	}
	if g2 == user.group {
		return ReasonIllegalState
	}
	source := s.groups[user.group]
	used := user.ent.usage
	if target.usage+used > target.hard {
		return ReasonGroupHardLimit
	}
	if used > 0 && nGrace(s, target, KindGroup, now) {
		return ReasonGroupGraceExpired
	}
	source.usage -= used
	target.usage += used
	user.group = g2
	nTidy(source, now)
	nTidy(target, now)
	s.last = now
	return -1
}

// __NAIVE_APPEND__
