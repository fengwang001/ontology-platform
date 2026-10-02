// Package quota implements a two-level (user and group) storage quota
// manager with soft/hard limits and per-entity grace-period timers.
package quota

import (
	"fmt"
	"sync"
)

// Kind identifies an entity level.
type Kind int

const (
	KindUser  Kind = iota // user entity
	KindGroup             // group entity
)

// RejectReason identifies why an operation was rejected.
type RejectReason int

const (
	ReasonInvalidArgument RejectReason = iota
	ReasonClockRollback
	ReasonNotFound
	ReasonIllegalState
	ReasonUserHardLimit
	ReasonUserGraceExpired
	ReasonGroupHardLimit
	ReasonGroupGraceExpired
)

// RejectError is returned for every rejected operation. Its Reason lets
// callers distinguish every documented rejection cause.
type RejectError struct {
	Reason  RejectReason
	message string
}

func (e *RejectError) Error() string { return e.message }

func errf(reason RejectReason, format string, args ...any) error {
	return &RejectError{Reason: reason, message: fmt.Sprintf(format, args...)}
}

type entity struct {
	kind    Kind
	id      int
	g       int // containing group id (users only)
	soft    int64
	hard    int64
	usage   int64
	graceAt int64 // grace start time; -1 when unset
}

// Manager is the concurrency-safe quota manager.
type Manager struct {
	mu sync.RWMutex

	gu int64
	gg int64

	users  map[int]*entity
	groups map[int]*entity

	// lastNow is the largest now accepted by a mutating operation.
	lastNow int64
}

const (
	maxID    = 1_000_000
	maxLimit = int64(1_000_000_000_000_000)
	maxGrace = int64(1_000_000_000)
	maxX     = int64(1_000_000_000_000)
	maxNow   = int64(1_000_000_000_000_000)
)

func validID(id int) bool { return 0 <= id && id <= maxID }

func validLimits(soft, hard int64) bool {
	return 0 <= soft && soft <= hard && hard <= maxLimit
}

func validNow(now int64) bool { return 0 <= now && now <= maxNow }

// New creates a Manager with user grace period gu and group grace period gg.
// It rejects the whole construction when either value is outside [1, 10^9].
func New(gu, gg int64) (*Manager, error) {
	if !(1 <= gu && gu <= maxGrace) || !(1 <= gg && gg <= maxGrace) {
		return nil, errf(ReasonInvalidArgument,
			"quota: grace periods must be in [1, 1e9], got gu=%d gg=%d", gu, gg)
	}
	return &Manager{
		gu:      gu,
		gg:      gg,
		users:   make(map[int]*entity),
		groups:  make(map[int]*entity),
		lastNow: -1,
	}, nil
}

// graceExpired reports whether the grace period of e has elapsed at now.
func (m *Manager) graceExpired(e *entity, now int64) bool {
	if e.graceAt < 0 {
		return false
	}
	window := m.gu
	if e.kind == KindGroup {
		window = m.gg
	}
	return now >= e.graceAt+window
}

// tidy applies the unified grace-bookkeeping rule: usage <= soft clears the
// start; otherwise a missing start becomes now and an existing one is kept.
func tidy(e *entity, now int64) {
	if e.usage <= e.soft {
		e.graceAt = -1
	} else if e.graceAt < 0 {
		e.graceAt = now
	}
}

func kindName(kind Kind) string {
	if kind == KindGroup {
		return "group"
	}
	return "user"
}

func (m *Manager) entityLocked(kind Kind, id int) (*entity, bool) {
	if kind == KindUser {
		e, ok := m.users[id]
		return e, ok
	}
	e, ok := m.groups[id]
	return e, ok
}

func (m *Manager) lookup(id int) (*entity, bool) {
	if e, ok := m.users[id]; ok {
		return e, true
	}
	e, ok := m.groups[id]
	return e, ok
}

// AddGroup registers a new group with the given limits and zero usage.
func (m *Manager) AddGroup(g int, soft, hard int64) error {
	if !validID(g) || !validLimits(soft, hard) {
		return errf(ReasonInvalidArgument,
			"quota: invalid AddGroup args g=%d soft=%d hard=%d", g, soft, hard)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[g]; ok {
		return errf(ReasonIllegalState, "quota: group %d already exists", g)
	}
	m.groups[g] = &entity{
		kind: KindGroup, id: g, soft: soft, hard: hard, graceAt: -1,
	}
	return nil
}

// AddUser registers a new user in an existing group.
func (m *Manager) AddUser(u, g int, soft, hard int64) error {
	if !validID(u) || !validID(g) || !validLimits(soft, hard) {
		return errf(ReasonInvalidArgument,
			"quota: invalid AddUser args u=%d g=%d soft=%d hard=%d", u, g, soft, hard)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u]; ok {
		return errf(ReasonIllegalState, "quota: user %d already exists", u)
	}
	if _, ok := m.groups[g]; !ok {
		return errf(ReasonNotFound, "quota: group %d does not exist", g)
	}
	m.users[u] = &entity{
		kind: KindUser, id: u, g: g, soft: soft, hard: hard, graceAt: -1,
	}
	return nil
}

// Alloc assigns x units to user u at time now. Checks run in fixed
// priority order: user hard limit, user grace expiry, group hard limit,
// group grace expiry.
func (m *Manager) Alloc(u int, x, now int64) error {
	if !validID(u) || x < 1 || x > maxX || !validNow(now) {
		return errf(ReasonInvalidArgument,
			"quota: invalid Alloc args u=%d x=%d now=%d", u, x, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return errf(ReasonClockRollback,
			"quota: clock rolled back: now=%d < last=%d", now, m.lastNow)
	}
	user, ok := m.users[u]
	if !ok {
		return errf(ReasonNotFound, "quota: user %d does not exist", u)
	}
	group := m.groups[user.g]
	if user.usage+x > user.hard {
		return errf(ReasonUserHardLimit,
			"quota: user %d hard limit: %d+%d > %d", u, user.usage, x, user.hard)
	}
	if m.graceExpired(user, now) {
		return errf(ReasonUserGraceExpired,
			"quota: user %d grace expired at %d (start=%d gu=%d)",
			u, now, user.graceAt, m.gu)
	}
	if group.usage+x > group.hard {
		return errf(ReasonGroupHardLimit,
			"quota: group %d hard limit: %d+%d > %d",
			group.id, group.usage, x, group.hard)
	}
	if m.graceExpired(group, now) {
		return errf(ReasonGroupGraceExpired,
			"quota: group %d grace expired at %d (start=%d gg=%d)",
			group.id, now, group.graceAt, m.gg)
	}
	user.usage += x
	group.usage += x
	tidy(user, now)
	tidy(group, now)
	m.lastNow = now
	return nil
}

// Free releases x units of user u at time now. Free never checks limits or
// grace and always re-tidies the user and its group.
func (m *Manager) Free(u int, x, now int64) error {
	if !validID(u) || x < 1 || x > maxX || !validNow(now) {
		return errf(ReasonInvalidArgument,
			"quota: invalid Free args u=%d x=%d now=%d", u, x, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return errf(ReasonClockRollback,
			"quota: clock rolled back: now=%d < last=%d", now, m.lastNow)
	}
	user, ok := m.users[u]
	if !ok {
		return errf(ReasonNotFound, "quota: user %d does not exist", u)
	}
	if x > user.usage {
		return errf(ReasonIllegalState,
			"quota: cannot free %d from user %d with usage %d", x, u, user.usage)
	}
	group := m.groups[user.g]
	user.usage -= x
	group.usage -= x
	tidy(user, now)
	tidy(group, now)
	m.lastNow = now
	return nil
}

// SetLimits changes the limits of an existing entity. Usage above the new
// hard limit is allowed; later allocations are then refused by the hard
// check. The entity is tidied once using the new limits.
func (m *Manager) SetLimits(kind Kind, id int, soft, hard, now int64) error {
	if (kind != KindUser && kind != KindGroup) || !validID(id) ||
		!validLimits(soft, hard) || !validNow(now) {
		return errf(ReasonInvalidArgument,
			"quota: invalid SetLimits args kind=%d id=%d soft=%d hard=%d now=%d",
			kind, id, soft, hard, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return errf(ReasonClockRollback,
			"quota: clock rolled back: now=%d < last=%d", now, m.lastNow)
	}
	e, ok := m.entityLocked(kind, id)
	if !ok {
		return errf(ReasonNotFound, "quota: %s %d does not exist", kindName(kind), id)
	}
	e.soft = soft
	e.hard = hard
	tidy(e, now)
	m.lastNow = now
	return nil
}

// Move moves user u into group g2 at time now. The target group sees an
// allocation of the user's current usage U (hard first, grace second, the
// grace check skipped when U == 0); the source group sees a release. The
// user entity itself is unchanged; both groups are tidied afterwards.
func (m *Manager) Move(u, g2 int, now int64) error {
	if !validID(u) || !validID(g2) || !validNow(now) {
		return errf(ReasonInvalidArgument,
			"quota: invalid Move args u=%d g2=%d now=%d", u, g2, now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return errf(ReasonClockRollback,
			"quota: clock rolled back: now=%d < last=%d", now, m.lastNow)
	}
	user, ok := m.users[u]
	if !ok {
		return errf(ReasonNotFound, "quota: user %d does not exist", u)
	}
	target, ok := m.groups[g2]
	if !ok {
		return errf(ReasonNotFound, "quota: target group %d does not exist", g2)
	}
	if g2 == user.g {
		return errf(ReasonIllegalState,
			"quota: user %d already belongs to group %d", u, g2)
	}
	source := m.groups[user.g]
	used := user.usage
	if target.usage+used > target.hard {
		return errf(ReasonGroupHardLimit,
			"quota: target group %d hard limit: %d+%d > %d",
			g2, target.usage, used, target.hard)
	}
	if used > 0 && m.graceExpired(target, now) {
		return errf(ReasonGroupGraceExpired,
			"quota: target group %d grace expired at %d (start=%d gg=%d)",
			g2, now, target.graceAt, m.gg)
	}
	source.usage -= used
	target.usage += used
	user.g = g2
	tidy(source, now)
	tidy(target, now)
	m.lastNow = now
	return nil
}

func graceOf(e *entity) (int64, bool) {
	if e.graceAt < 0 {
		return 0, false
	}
	return e.graceAt, true
}

// Grace returns the grace start time of an entity and whether it is set.
// Ids present as both a user and a group resolve to the user; use
// UserGrace/GroupGrace to select the level explicitly.
func (m *Manager) Grace(id int) (int64, bool, error) {
	if !validID(id) {
		return 0, false, errf(ReasonInvalidArgument, "quota: invalid id %d", id)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.lookup(id)
	if !ok {
		return 0, false, errf(ReasonNotFound, "quota: entity %d does not exist", id)
	}
	t, set := graceOf(e)
	return t, set, nil
}

// UserGrace returns a user's grace start time.
func (m *Manager) UserGrace(u int) (int64, bool, error) {
	if !validID(u) {
		return 0, false, errf(ReasonInvalidArgument, "quota: invalid user id %d", u)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.users[u]
	if !ok {
		return 0, false, errf(ReasonNotFound, "quota: user %d does not exist", u)
	}
	t, set := graceOf(e)
	return t, set, nil
}

// GroupGrace returns a group's grace start time.
func (m *Manager) GroupGrace(g int) (int64, bool, error) {
	if !validID(g) {
		return 0, false, errf(ReasonInvalidArgument, "quota: invalid group id %d", g)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.groups[g]
	if !ok {
		return 0, false, errf(ReasonNotFound, "quota: group %d does not exist", g)
	}
	t, set := graceOf(e)
	return t, set, nil
}

// Usage returns the current usage of an entity. Ids present as both a user
// and a group resolve to the user; use UserUsage/GroupUsage explicitly.
func (m *Manager) Usage(id int) (int64, error) {
	if !validID(id) {
		return 0, errf(ReasonInvalidArgument, "quota: invalid id %d", id)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.lookup(id)
	if !ok {
		return 0, errf(ReasonNotFound, "quota: entity %d does not exist", id)
	}
	return e.usage, nil
}

// UserUsage returns a user's usage.
func (m *Manager) UserUsage(u int) (int64, error) {
	if !validID(u) {
		return 0, errf(ReasonInvalidArgument, "quota: invalid user id %d", u)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.users[u]
	if !ok {
		return 0, errf(ReasonNotFound, "quota: user %d does not exist", u)
	}
	return e.usage, nil
}

// GroupUsage returns a group's usage.
func (m *Manager) GroupUsage(g int) (int64, error) {
	if !validID(g) {
		return 0, errf(ReasonInvalidArgument, "quota: invalid group id %d", g)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.groups[g]
	if !ok {
		return 0, errf(ReasonNotFound, "quota: group %d does not exist", g)
	}
	return e.usage, nil
}
