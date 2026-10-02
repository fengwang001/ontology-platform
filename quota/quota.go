package quota

import (
	"errors"
	"sync"
)

type EntityKind uint8

const (
	UserKind EntityKind = iota + 1
	GroupKind
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrEntityExists      = errors.New("entity already exists")
	ErrEntityNotFound    = errors.New("entity not found")
	ErrClockRewound      = errors.New("clock rewound")
	ErrOverRelease       = errors.New("free amount exceeds user usage")
	ErrSameGroup         = errors.New("user is already in target group")
	ErrUserHardLimit     = errors.New("user hard limit exceeded")
	ErrUserGraceExpired  = errors.New("user grace period expired")
	ErrGroupHardLimit    = errors.New("group hard limit exceeded")
	ErrGroupGraceExpired = errors.New("group grace period expired")
)

type entity struct {
	kind     EntityKind
	groupID  int64
	soft     int64
	hard     int64
	usage    int64
	hasGrace bool
	graceAt  int64
}

type Manager struct {
	mu         sync.RWMutex
	userGrace  int64
	groupGrace int64
	latestNow  int64
	users      map[int64]*entity
	groups     map[int64]*entity
}

func NewManager(userGrace, groupGrace int64) (*Manager, error) {
	if userGrace < 1 || userGrace > 1_000_000_000 ||
		groupGrace < 1 || groupGrace > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}

	return &Manager{
		userGrace:  userGrace,
		groupGrace: groupGrace,
		users:      make(map[int64]*entity),
		groups:     make(map[int64]*entity),
	}, nil
}

func (m *Manager) AddGroup(id, soft, hard int64) error {
	if !validID(id) || !validLimits(soft, hard) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[id]; ok {
		return ErrEntityExists
	}

	m.groups[id] = &entity{
		kind: GroupKind,
		soft: soft,
		hard: hard,
	}
	return nil
}

func (m *Manager) AddUser(id, groupID, soft, hard int64) error {
	if !validID(id) || !validID(groupID) || !validLimits(soft, hard) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.users[id]; ok {
		return ErrEntityExists
	}
	_, ok := m.groups[groupID]
	if !ok {
		return ErrEntityNotFound
	}

	m.users[id] = &entity{
		kind:    UserKind,
		groupID: groupID,
		soft:    soft,
		hard:    hard,
	}
	return nil
}

func (m *Manager) Alloc(userID, amount, now int64) error {
	if !validID(userID) || !validAmount(amount) || !validNow(now) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.latestNow {
		return ErrClockRewound
	}
	user, ok := m.users[userID]
	if !ok {
		return ErrEntityNotFound
	}
	group := m.groups[user.groupID]

	if user.usage+amount > user.hard {
		return ErrUserHardLimit
	}
	if m.graceExpired(user, now) {
		return ErrUserGraceExpired
	}
	if group.usage+amount > group.hard {
		return ErrGroupHardLimit
	}
	if m.graceExpired(group, now) {
		return ErrGroupGraceExpired
	}

	m.latestNow = now
	user.usage += amount
	group.usage += amount
	m.tidy(user, now)
	m.tidy(group, now)
	return nil
}

func (m *Manager) Free(userID, amount, now int64) error {
	if !validID(userID) || !validAmount(amount) || !validNow(now) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.latestNow {
		return ErrClockRewound
	}
	user, ok := m.users[userID]
	if !ok {
		return ErrEntityNotFound
	}
	if amount > user.usage {
		return ErrOverRelease
	}
	group := m.groups[user.groupID]

	m.latestNow = now
	user.usage -= amount
	group.usage -= amount
	m.tidy(user, now)
	m.tidy(group, now)
	return nil
}

func (m *Manager) SetLimits(kind EntityKind, id, soft, hard, now int64) error {
	if (kind != UserKind && kind != GroupKind) ||
		!validID(id) || !validLimits(soft, hard) || !validNow(now) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.latestNow {
		return ErrClockRewound
	}
	var current *entity
	var ok bool
	if kind == UserKind {
		current, ok = m.users[id]
	} else {
		current, ok = m.groups[id]
	}
	if !ok {
		return ErrEntityNotFound
	}

	m.latestNow = now
	if current.soft == soft && current.hard == hard {
		return nil
	}
	current.soft = soft
	current.hard = hard
	m.tidy(current, now)
	return nil
}

func (m *Manager) Move(userID, targetGroupID, now int64) error {
	if !validID(userID) || !validID(targetGroupID) || !validNow(now) {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.latestNow {
		return ErrClockRewound
	}
	user, ok := m.users[userID]
	if !ok {
		return ErrEntityNotFound
	}
	target, ok := m.groups[targetGroupID]
	if !ok {
		return ErrEntityNotFound
	}
	if user.groupID == targetGroupID {
		return ErrSameGroup
	}

	source := m.groups[user.groupID]
	userUsage := user.usage
	if target.usage+userUsage > target.hard {
		return ErrGroupHardLimit
	}
	if userUsage > 0 && m.graceExpired(target, now) {
		return ErrGroupGraceExpired
	}

	m.latestNow = now
	source.usage -= userUsage
	target.usage += userUsage
	user.groupID = targetGroupID
	m.tidy(source, now)
	m.tidy(target, now)
	return nil
}

func (m *Manager) Usage(id int64) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current, ok := m.lookup(id)
	if !ok {
		return 0, ErrEntityNotFound
	}
	return current.usage, nil
}

func (m *Manager) Grace(id int64) (int64, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current, ok := m.lookup(id)
	if !ok {
		return 0, false, ErrEntityNotFound
	}
	return current.graceAt, current.hasGrace, nil
}

func (m *Manager) lookup(id int64) (*entity, bool) {
	if current, ok := m.users[id]; ok {
		return current, true
	}
	current, ok := m.groups[id]
	return current, ok
}

func validID(id int64) bool {
	return id >= 0 && id <= 1_000_000
}

func validLimits(soft, hard int64) bool {
	return soft >= 0 && hard <= 1_000_000_000_000_000 && soft <= hard
}

func validAmount(amount int64) bool {
	return amount >= 1 && amount <= 1_000_000_000_000
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000
}

func (m *Manager) graceExpired(current *entity, now int64) bool {
	if !current.hasGrace {
		return false
	}
	graceDuration := m.userGrace
	if current.kind == GroupKind {
		graceDuration = m.groupGrace
	}
	return now >= current.graceAt+graceDuration
}

func (m *Manager) tidy(current *entity, now int64) {
	if current.usage <= current.soft {
		current.hasGrace = false
		current.graceAt = 0
		return
	}
	if !current.hasGrace {
		current.hasGrace = true
		current.graceAt = now
	}
}
