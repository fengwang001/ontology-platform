package yard_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/yard"
)

type testOp struct {
	kind  string
	truck string
	dock  string
	ttype yard.Kind
	start int64
	now   int64
}

func (op testOp) String() string {
	return fmt.Sprintf("%s(truck=%q dock=%q type=%d s=%d now=%d)",
		op.kind, op.truck, op.dock, op.ttype, op.start, op.now)
}

func (op testOp) run(s *yard.Scheduler) ([]yard.Assignment, error) {
	switch op.kind {
	case "book":
		return s.Book([]byte(op.truck), op.ttype, op.start, op.now)
	case "dock":
		return s.AddDock([]byte(op.dock), op.ttype, op.now)
	case "checkin":
		return s.CheckIn([]byte(op.truck), op.ttype, op.now)
	default:
		return s.Depart([]byte(op.truck), op.now)
	}
}

type randomEnv struct {
	rng    *rand.Rand
	S      int64
	E      int64
	L      int64
	W      int64
	K      int64
	now    int64
	trucks []string
	docks  []string
}

func newRandomEnv(rng *rand.Rand) randomEnv {
	return randomEnv{
		rng: rng,
		S:   int64(rng.Intn(20) + 1),
		E:   int64(rng.Intn(40) + 1),
		L:   int64(rng.Intn(40) + 1),
		W:   int64(rng.Intn(80) + 1),
		K:   int64(rng.Intn(4) + 1),
		now: 0,
	}
}

func (e *randomEnv) randomOp(rng *rand.Rand, step int) testOp {
	if e.now < 500 {
		e.now += int64(rng.Intn(8))
	}
	choice := rng.Intn(100)
	op := testOp{now: e.now, ttype: yard.Kind(rng.Intn(2))}
	switch {
	case choice < 25:
		op.kind = "book"
		op.truck = e.truckName(rng.Intn(18))
		op.start = (e.now + int64(rng.Intn(5))*e.S) / e.S * e.S
	case choice < 45:
		op.kind = "dock"
		op.dock = e.dockName(rng.Intn(12))
	case choice < 80:
		op.kind = "checkin"
		op.truck = e.truckName(rng.Intn(24))
	default:
		op.kind = "depart"
		if len(e.trucks) > 0 {
			op.truck = e.trucks[rng.Intn(len(e.trucks))]
		} else {
			op.truck = e.truckName(0)
		}
	}
	_ = step
	return op
}

func (e *randomEnv) truckName(index int) string {
	for len(e.trucks) <= index {
		e.trucks = append(e.trucks, fmt.Sprintf("T%02d", len(e.trucks)))
	}
	return e.trucks[index]
}

func (e *randomEnv) dockName(index int) string {
	for len(e.docks) <= index {
		e.docks = append(e.docks, fmt.Sprintf("D%02d", len(e.docks)))
	}
	return e.docks[index]
}

type naiveModel struct {
	S, E, L, W, K int64
	maxNow        int64
	books         map[string]naiveBook
	docks         map[string]yard.Kind
	waits         map[string]naiveWait
	assigned      map[string]string
	seen          map[string]bool
	seq           int64
}

type naiveBook struct {
	kind  yard.Kind
	start int64
	state string
}

type naiveWait struct {
	kind    yard.Kind
	seq     int64
	checkin int64
	start   int64
	onTime  bool
}

func newNaiveModel(S, E, L, W, K int64) *naiveModel {
	return &naiveModel{
		S: S, E: E, L: L, W: W, K: K,
		books:    make(map[string]naiveBook),
		docks:    make(map[string]yard.Kind),
		waits:    make(map[string]naiveWait),
		assigned: make(map[string]string),
		seen:     make(map[string]bool),
	}
}

func (m *naiveModel) run(op testOp) ([]yard.Assignment, error) {
	if err := m.validate(op); err != nil {
		return nil, err
	}
	if op.now < m.maxNow {
		return nil, yard.ErrClockRollback
	}

	switch op.kind {
	case "book":
		if _, exists := m.books[op.truck]; exists {
			return nil, yard.ErrConflict
		}
		count := 0
		for _, book := range m.books {
			if book.kind == op.ttype && book.start == op.start {
				count++
			}
		}
		if int64(count) >= m.K {
			return nil, yard.ErrCapacity
		}
		m.books[op.truck] = naiveBook{kind: op.ttype, start: op.start, state: "active"}
	case "dock":
		if _, exists := m.docks[op.dock]; exists {
			return nil, yard.ErrConflict
		}
		m.docks[op.dock] = op.ttype
	case "checkin":
		if m.seen[op.truck] {
			return nil, yard.ErrState
		}
		book, hasBook := m.books[op.truck]
		if hasBook {
			if book.kind != op.ttype {
				return nil, yard.ErrState
			}
			onTime := book.start-op.now <= m.E && op.now-book.start <= m.L
			if onTime {
				book.state = "consumed"
			} else {
				book.state = "void"
			}
			m.books[op.truck] = book
			m.waits[op.truck] = naiveWait{
				kind: op.ttype, seq: m.seq, checkin: op.now,
				start: book.start, onTime: onTime,
			}
		} else {
			m.waits[op.truck] = naiveWait{kind: op.ttype, seq: m.seq, checkin: op.now}
		}
		m.seq++
		m.seen[op.truck] = true
	case "depart":
		_, ok := m.assigned[op.truck]
		if !ok {
			if !m.seen[op.truck] {
				return nil, yard.ErrNotFound
			}
			return nil, yard.ErrState
		}
		delete(m.assigned, op.truck)
	}
	m.maxNow = op.now
	return m.dispatch(op.now), nil
}

func (m *naiveModel) validate(op testOp) error {
	if op.now < 0 || op.now > 1_000_000_000 || (op.ttype != yard.Dry && op.ttype != yard.Reefer) {
		return yard.ErrInvalidArgument
	}
	if op.kind == "book" {
		if len(op.truck) < 1 || len(op.truck) > 32 || op.start < 0 ||
			op.start < op.now || op.start%m.S != 0 {
			return yard.ErrInvalidArgument
		}
	}
	if op.kind == "dock" && (len(op.dock) < 1 || len(op.dock) > 32) {
		return yard.ErrInvalidArgument
	}
	if (op.kind == "checkin" || op.kind == "depart") &&
		(len(op.truck) < 1 || len(op.truck) > 32) {
		return yard.ErrInvalidArgument
	}
	return nil
}

func (m *naiveModel) dispatch(now int64) []yard.Assignment {
	result := make([]yard.Assignment, 0)
	for {
		waiting := m.orderedWaiters(now)
		freeDry, freeReefer := m.freeDocks()
		chosen := -1
		var chosenDock string
		for i, truck := range waiting {
			wait := m.waits[truck]
			switch {
			case wait.kind == yard.Reefer && len(freeReefer) > 0:
				chosen, chosenDock = i, freeReefer[0]
			case wait.kind == yard.Dry && len(freeDry) > 0:
				chosen, chosenDock = i, freeDry[0]
			case wait.kind == yard.Dry && len(freeReefer) > 0 && !m.anyKind(yard.Reefer, waiting):
				chosen, chosenDock = i, freeReefer[0]
			}
			if chosen >= 0 {
				break
			}
		}
		if chosen < 0 {
			return result
		}
		truck := waiting[chosen]
		delete(m.waits, truck)
		m.assigned[truck] = chosenDock
		result = append(result, yard.Assignment{Truck: []byte(truck), Dock: []byte(chosenDock)})
	}
}

func (m *naiveModel) orderedWaiters(now int64) []string {
	type key struct {
		tier  int
		start int64
		seq   int64
	}
	items := make(map[string]key)
	ids := make([]string, 0)
	for truck, wait := range m.waits {
		tier := 2
		if !wait.onTime {
			if elapsed := now - wait.checkin; elapsed >= 0 && elapsed >= m.W {
				tier = 0
			}
		} else {
			tier = 1
		}
		items[truck] = key{tier, wait.start, wait.seq}
		ids = append(ids, truck)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := items[ids[i]], items[ids[j]]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.tier == 1 && a.start != b.start {
			return a.start < b.start
		}
		return a.seq < b.seq
	})
	return ids
}

func (m *naiveModel) freeDocks() ([]string, []string) {
	occupied := make(map[string]bool)
	for truck := range m.assigned {
		occupied[m.assigned[truck]] = true
	}
	var dry, reefer []string
	for dockID, kind := range m.docks {
		if occupied[dockID] {
			continue
		}
		if kind == yard.Dry {
			dry = append(dry, dockID)
		} else {
			reefer = append(reefer, dockID)
		}
	}
	sort.Strings(dry)
	sort.Strings(reefer)
	return dry, reefer
}

func (m *naiveModel) anyKind(kind yard.Kind, trucks []string) bool {
	for _, truck := range trucks {
		if m.waits[truck].kind == kind {
			return true
		}
	}
	return false
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return errors.Is(got, want)
}

func TestRandomSequencesAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 1500; iteration++ {
		env := newRandomEnv(rng)
		s, err := yard.NewScheduler(env.S, env.E, env.L, env.W, env.K)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaiveModel(env.S, env.E, env.L, env.W, env.K)
		var log strings.Builder
		fmt.Fprintf(&log, "iteration=%d input params S=%d E=%d L=%d W=%d K=%d\n",
			iteration, env.S, env.E, env.L, env.W, env.K)

		for step := 0; step < 80; step++ {
			op := env.randomOp(rng, step)
			fmt.Fprintf(&log, "input %d: %s\n", step, op)
			got, gotErr := op.run(s)
			want, wantErr := model.run(op)
			gotPairs := pairs(got)
			wantPairs := pairs(want)
			fmt.Fprintf(&log, "output got=%v err=%v; reference=%v err=%v\n",
				gotPairs, gotErr, wantPairs, wantErr)
			if !sameError(gotErr, wantErr) || !reflectDeepEqualPairs(gotPairs, wantPairs) {
				t.Fatalf("mismatch; judgment=naive full priority scan\n%s", log.String())
			}
		}
	}
}

func pairs(list []yard.Assignment) []assignmentPair {
	result := make([]assignmentPair, 0, len(list))
	for _, item := range list {
		result = append(result, assignmentPair{string(item.Truck), string(item.Dock)})
	}
	return result
}

func reflectDeepEqualPairs(a, b []assignmentPair) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}
