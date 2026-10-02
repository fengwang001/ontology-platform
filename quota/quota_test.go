package quota

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestConstruction(t *testing.T) {
	if _, err := NewManager(100, 200); err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
}

type simEntity struct {
	kind     EntityKind
	groupID  int64
	soft     int64
	hard     int64
	usage    int64
	hasGrace bool
	graceAt  int64
}

type naiveManager struct {
	userGrace  int64
	groupGrace int64
	latestNow  int64
	users      map[int64]simEntity
	groups     map[int64]simEntity
}

type opKind uint8

const (
	opAlloc opKind = iota + 1
	opFree
	opSetLimits
	opMove
)

type testOp struct {
	kind   opKind
	id     int64
	target int64
	amount int64
	soft   int64
	hard   int64
	now    int64
}

func TestRandomDifferential(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(fmt.Sprintf("sequence_%04d", sequence), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(sequence+1), uint64(sequence+9001)))
			mgr, err := NewManager(int64(rng.IntN(9))+2, int64(rng.IntN(19))+2)
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}
			sim := newNaiveManager(mgr.userGrace, mgr.groupGrace)

			for groupID := int64(0); groupID < 4; groupID++ {
				soft := int64(rng.IntN(180))
				hard := soft + int64(rng.IntN(220))
				runOne(t, mgr, sim, "AddGroup", testOp{
					id: groupID, soft: soft, hard: hard,
				}, func() error {
					return mgr.AddGroup(groupID, soft, hard)
				}, func() error {
					return sim.addGroup(groupID, soft, hard)
				})
			}
			for userID := int64(10); userID < 16; userID++ {
				groupID := int64(rng.IntN(4))
				soft := int64(rng.IntN(120))
				hard := soft + int64(rng.IntN(120))
				runOne(t, mgr, sim, "AddUser", testOp{
					id: userID, target: groupID, soft: soft, hard: hard,
				}, func() error {
					return mgr.AddUser(userID, groupID, soft, hard)
				}, func() error {
					return sim.addUser(userID, groupID, soft, hard)
				})
			}

			now := int64(0)
			for step := 0; step < 180; step++ {
				now += int64(rng.IntN(8))
				operation := testOp{
					kind: opKind(rng.IntN(4) + 1),
					id:   int64(rng.IntN(10)),
					now:  now,
				}
				switch operation.kind {
				case opAlloc:
					operation.amount = int64(rng.IntN(90)) + 1
					if rng.IntN(12) == 0 {
						operation.id = int64(rng.IntN(20))
					} else {
						operation.id = int64(10 + rng.IntN(6))
					}
					kind := EntityKind(rng.IntN(2) + 1)
					runOp(t, mgr, sim, "Alloc", kind, operation,
						func() error { return mgr.Alloc(operation.id, operation.amount, operation.now) },
						func() error { return sim.alloc(operation.id, operation.amount, operation.now) })
				case opFree:
					operation.amount = int64(rng.IntN(70)) + 1
					operation.id = int64(10 + rng.IntN(6))
					if rng.IntN(12) == 0 {
						operation.id = int64(rng.IntN(20))
					}
					kind := EntityKind(rng.IntN(2) + 1)
					runOp(t, mgr, sim, "Free", kind, operation,
						func() error { return mgr.Free(operation.id, operation.amount, operation.now) },
						func() error { return sim.free(operation.id, operation.amount, operation.now) })
				case opSetLimits:
					operation.soft = int64(rng.IntN(220))
					operation.hard = operation.soft + int64(rng.IntN(220))
					operation.id = int64(rng.IntN(16))
					if operation.id < 4 {
						operation.kind = opKind(GroupKind)
					} else if operation.id >= 10 {
						operation.kind = opKind(UserKind)
					} else {
						operation.kind = opKind(rng.IntN(2) + 1)
					}
					kind := EntityKind(operation.kind)
					runOp(t, mgr, sim, "SetLimits", kind, operation,
						func() error {
							return mgr.SetLimits(kind, operation.id, operation.soft, operation.hard, operation.now)
						},
						func() error {
							return sim.setLimits(kind, operation.id, operation.soft, operation.hard, operation.now)
						})
				case opMove:
					operation.id = int64(10 + rng.IntN(6))
					operation.target = int64(rng.IntN(5))
					if rng.IntN(12) == 0 {
						operation.id = int64(rng.IntN(20))
					}
					kind := EntityKind(rng.IntN(2) + 1)
					runOp(t, mgr, sim, "Move", kind, operation,
						func() error { return mgr.Move(operation.id, operation.target, operation.now) },
						func() error { return sim.move(operation.id, operation.target, operation.now) })
				}
			}
		})
	}
}

func runOne(
	t *testing.T,
	mgr *Manager,
	sim *naiveManager,
	name string,
	operation testOp,
	actual func() error,
	simCall func() error,
) {
	t.Helper()
	simErr := simCall()
	actualErr := actual()
	logOp(t, name, operation, actualErr, simErr)
	if !errors.Is(actualErr, simErr) {
		t.Fatalf("%s error = %v, want %v", name, actualErr, simErr)
	}
	assertStatesEqual(t, mgr, sim)
}

func runOp(
	t *testing.T,
	mgr *Manager,
	sim *naiveManager,
	name string,
	kind EntityKind,
	operation testOp,
	actual func() error,
	simCall func() error,
) {
	t.Helper()
	actualErr := actual()
	simErr := simCall()
	logOp(t, name, operation, actualErr, simErr)
	if !errors.Is(actualErr, simErr) {
		t.Fatalf("%s(%s, %+v) error = %v, want %v", name, kindName(kind), operation, actualErr, simErr)
	}
	assertStatesEqual(t, mgr, sim)
}

func logOp(t *testing.T, name string, operation testOp, actualErr, simErr error) {
	t.Helper()
	reason := "accepted"
	if actualErr != nil {
		reason = reasonName(actualErr)
	}
	t.Logf("input=%s %+v output=%v decision=%s reference=%v",
		name, operation, actualErr, reason, simErr)
}

func assertStatesEqual(t *testing.T, mgr *Manager, sim *naiveManager) {
	t.Helper()
	for id, expected := range sim.groups {
		actual, ok := mgr.groups[id]
		if !ok {
			t.Fatalf("group %d missing", id)
		}
		assertEntityEqual(t, "group", id, actual, expected)
	}
	for id, expected := range sim.users {
		actual, ok := mgr.users[id]
		if !ok {
			t.Fatalf("user %d missing", id)
		}
		assertEntityEqual(t, "user", id, actual, expected)
	}
	for id := range mgr.groups {
		if _, ok := sim.groups[id]; !ok {
			t.Fatalf("unexpected group %d", id)
		}
	}
	for id := range mgr.users {
		if _, ok := sim.users[id]; !ok {
			t.Fatalf("unexpected user %d", id)
		}
	}
	if mgr.latestNow != sim.latestNow {
		t.Fatalf("latestNow = %d, want %d", mgr.latestNow, sim.latestNow)
	}
}

func assertEntityEqual(t *testing.T, label string, id int64, actual *entity, expected simEntity) {
	t.Helper()
	if actual.kind != expected.kind ||
		actual.groupID != expected.groupID ||
		actual.soft != expected.soft ||
		actual.hard != expected.hard ||
		actual.usage != expected.usage ||
		actual.hasGrace != expected.hasGrace ||
		actual.graceAt != expected.graceAt {
		t.Fatalf("%s %d = %+v, want %+v", label, id, actual, expected)
	}
}

func kindName(kind EntityKind) string {
	if kind == GroupKind {
		return "group"
	}
	return "user"
}

func reasonName(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid argument"
	case errors.Is(err, ErrClockRewound):
		return "clock rewound"
	case errors.Is(err, ErrEntityExists):
		return "entity exists"
	case errors.Is(err, ErrEntityNotFound):
		return "entity not found"
	case errors.Is(err, ErrOverRelease):
		return "over release"
	case errors.Is(err, ErrSameGroup):
		return "same group"
	case errors.Is(err, ErrUserHardLimit):
		return "user hard limit"
	case errors.Is(err, ErrUserGraceExpired):
		return "user grace expired"
	case errors.Is(err, ErrGroupHardLimit):
		return "group hard limit"
	case errors.Is(err, ErrGroupGraceExpired):
		return "group grace expired"
	default:
		return err.Error()
	}
}

func newNaiveManager(ug, gg int64) *naiveManager {
	return &naiveManager{
		userGrace:  ug,
		groupGrace: gg,
		users:      make(map[int64]simEntity),
		groups:     make(map[int64]simEntity),
	}
}

func simValidID(id int64) bool {
	return id >= 0 && id <= 1_000_000
}

func simValidLimits(soft, hard int64) bool {
	return soft >= 0 && hard <= 1_000_000_000_000_000 && soft <= hard
}

func simValidAmount(amount int64) bool {
	return amount >= 1 && amount <= 1_000_000_000_000
}

func simValidNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000
}

var errSimInvalid = errors.New("sim invalid")

func (s *naiveManager) tidy(current *simEntity, now int64) {
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

func (s *naiveManager) expired(current simEntity, now int64) bool {
	if !current.hasGrace {
		return false
	}
	duration := s.userGrace
	if current.kind == GroupKind {
		duration = s.groupGrace
	}
	return now >= current.graceAt+duration
}

func (s *naiveManager) addGroup(id, soft, hard int64) error {
	if !simValidID(id) || !simValidLimits(soft, hard) {
		return ErrInvalidArgument
	}
	if _, exists := s.groups[id]; exists {
		return ErrEntityExists
	}
	s.groups[id] = simEntity{kind: GroupKind, soft: soft, hard: hard}
	return nil
}

func (s *naiveManager) addUser(id, groupID, soft, hard int64) error {
	if !simValidID(id) || !simValidID(groupID) || !simValidLimits(soft, hard) {
		return ErrInvalidArgument
	}
	if _, exists := s.users[id]; exists {
		return ErrEntityExists
	}
	_, ok := s.groups[groupID]
	if !ok {
		return ErrEntityNotFound
	}
	s.users[id] = simEntity{
		kind:    UserKind,
		groupID: groupID,
		soft:    soft,
		hard:    hard,
	}
	return nil
}

func (s *naiveManager) alloc(id, amount, now int64) error {
	if !simValidID(id) || !simValidAmount(amount) || !simValidNow(now) {
		return ErrInvalidArgument
	}
	if now < s.latestNow {
		return ErrClockRewound
	}
	user, ok := s.users[id]
	if !ok {
		return ErrEntityNotFound
	}
	group := s.groups[user.groupID]
	groupID := user.groupID

	if user.usage+amount > user.hard {
		return ErrUserHardLimit
	}
	if s.expired(user, now) {
		return ErrUserGraceExpired
	}
	if group.usage+amount > group.hard {
		return ErrGroupHardLimit
	}
	if s.expired(group, now) {
		return ErrGroupGraceExpired
	}

	s.latestNow = now
	user.usage += amount
	group.usage += amount
	s.tidy(&user, now)
	s.tidy(&group, now)
	s.users[id] = user
	s.groups[groupID] = group
	return nil
}

func (s *naiveManager) free(id, amount, now int64) error {
	if !simValidID(id) || !simValidAmount(amount) || !simValidNow(now) {
		return ErrInvalidArgument
	}
	if now < s.latestNow {
		return ErrClockRewound
	}
	user, ok := s.users[id]
	if !ok {
		return ErrEntityNotFound
	}
	if amount > user.usage {
		return ErrOverRelease
	}
	group := s.groups[user.groupID]
	groupID := user.groupID

	s.latestNow = now
	user.usage -= amount
	group.usage -= amount
	s.tidy(&user, now)
	s.tidy(&group, now)
	s.users[id] = user
	s.groups[groupID] = group
	return nil
}

func (s *naiveManager) setLimits(kind EntityKind, id, soft, hard, now int64) error {
	if (kind != UserKind && kind != GroupKind) ||
		!simValidID(id) || !simValidLimits(soft, hard) || !simValidNow(now) {
		return ErrInvalidArgument
	}
	if now < s.latestNow {
		return ErrClockRewound
	}
	var (
		current simEntity
		ok      bool
	)
	if kind == UserKind {
		current, ok = s.users[id]
	} else {
		current, ok = s.groups[id]
	}
	if !ok {
		return ErrEntityNotFound
	}

	s.latestNow = now
	if current.soft != soft || current.hard != hard {
		current.soft = soft
		current.hard = hard
		s.tidy(&current, now)
	}
	if kind == UserKind {
		s.users[id] = current
	} else {
		s.groups[id] = current
	}
	return nil
}

func (s *naiveManager) move(id, targetGroupID, now int64) error {
	if !simValidID(id) || !simValidID(targetGroupID) || !simValidNow(now) {
		return ErrInvalidArgument
	}
	if now < s.latestNow {
		return ErrClockRewound
	}
	user, ok := s.users[id]
	if !ok {
		return ErrEntityNotFound
	}
	target, exists := s.groups[targetGroupID]
	if !exists {
		return ErrEntityNotFound
	}
	if user.groupID == targetGroupID {
		return ErrSameGroup
	}
	source := s.groups[user.groupID]
	sourceGroupID := user.groupID

	if target.usage+user.usage > target.hard {
		return ErrGroupHardLimit
	}
	if user.usage > 0 && s.expired(target, now) {
		return ErrGroupGraceExpired
	}

	s.latestNow = now
	source.usage -= user.usage
	target.usage += user.usage
	user.groupID = targetGroupID
	s.tidy(&source, now)
	s.tidy(&target, now)
	s.users[id] = user
	s.groups[sourceGroupID] = source
	s.groups[targetGroupID] = target
	return nil
}

func (s *naiveManager) usage(id int64) (int64, bool) {
	current, ok := s.users[id]
	if !ok {
		current, ok = s.groups[id]
	}
	if !ok {
		return 0, false
	}
	return current.usage, true
}

func (s *naiveManager) grace(id int64) (int64, bool, bool) {
	current, ok := s.users[id]
	if !ok {
		current, ok = s.groups[id]
	}
	if !ok {
		return 0, false, false
	}
	return current.graceAt, current.hasGrace, true
}
