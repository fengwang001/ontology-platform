package ledger

import (
	"errors"
	"math/rand"
	"testing"
)

type oracleBlock struct {
	id        int64
	size      int64
	b         uint64
	d         uint64
	discarded bool
}

type oracleSnapshot struct {
	name string
	t    uint64
	hold int
}

type oracle struct {
	capacity int64
	cur      uint64
	nextID   int64
	blocks   map[int64]oracleBlock
	snaps    map[string]oracleSnapshot
}

type testOp struct {
	kind string
	i    int64
	name string
}

func newOracle(capacity int64) *oracle {
	return &oracle{
		capacity: capacity,
		cur:      1,
		nextID:   1,
		blocks:   make(map[int64]oracleBlock),
		snaps:    make(map[string]oracleSnapshot),
	}
}

func (m *oracle) contains(blk oracleBlock, t uint64) bool {
	return !blk.discarded && blk.b <= t && t < blk.d
}

func (m *oracle) used() int64 {
	total := int64(0)
	for _, blk := range m.blocks {
		if blk.discarded {
			continue
		}
		if blk.d == infinityTxn {
			total += blk.size
			continue
		}
		for _, snap := range m.snaps {
			if m.contains(blk, snap.t) {
				total += blk.size
				break
			}
		}
	}
	return total
}

func (m *oracle) liveBytes() int64 {
	total := int64(0)
	for _, blk := range m.blocks {
		if !blk.discarded && blk.d == infinityTxn {
			total += blk.size
		}
	}
	return total
}

func (m *oracle) referenced(name string) (int64, bool) {
	snap, ok := m.snaps[name]
	if !ok {
		return 0, false
	}
	total := int64(0)
	for _, blk := range m.blocks {
		if m.contains(blk, snap.t) {
			total += blk.size
		}
	}
	return total, true
}

func (m *oracle) unique(name string) (int64, bool) {
	target, ok := m.snaps[name]
	if !ok {
		return 0, false
	}
	total := int64(0)
	for _, blk := range m.blocks {
		if blk.discarded || blk.d == infinityTxn || !m.contains(blk, target.t) {
			continue
		}
		only := true
		for otherName, other := range m.snaps {
			if otherName != name && m.contains(blk, other.t) {
				only = false
				break
			}
		}
		if only {
			total += blk.size
		}
	}
	return total, true
}

func compareStates(t *testing.T, l *Ledger, m *oracle, names []string) {
	t.Helper()
	if l.cur != m.cur || l.nextID != m.nextID || l.Cap() != m.capacity {
		t.Fatalf("scalar state=(cur:%d next:%d cap:%d), want=(cur:%d next:%d cap:%d)",
			l.cur, l.nextID, l.Cap(), m.cur, m.nextID, m.capacity)
	}
	if got := l.Used(); got != m.used() || got > l.Cap() {
		t.Fatalf("Used()=%d oracle=%d cap=%d", got, m.used(), l.Cap())
	}
	activeCount := 0
	for _, blk := range m.blocks {
		if !blk.discarded {
			activeCount++
		}
	}
	if len(l.blocks) != activeCount {
		t.Fatalf("active block count=%d, want=%d", len(l.blocks), activeCount)
	}
	for id, want := range m.blocks {
		if want.discarded {
			if _, ok := l.blocks[id]; ok {
				t.Fatalf("discarded block %d remains in real ledger", id)
			}
			continue
		}
		got, ok := l.blocks[id]
		if !ok {
			t.Fatalf("real ledger missing block %d", id)
		}
		if got.size != want.size || got.b != want.b {
			t.Fatalf("block %d=(size:%d b:%d d:%d), want=(size:%d b:%d d:%d discarded:%v)",
				id, got.size, got.b, got.d, want.size, want.b, want.d, want.discarded)
		}
		if got.d != want.d {
			t.Fatalf("block %d d=%d, want %d", id, got.d, want.d)
		}
	}
	if len(l.snaps) != len(m.snaps) {
		t.Fatalf("snapshot count=%d, want=%d", len(l.snaps), len(m.snaps))
	}
	for _, name := range names {
		gotSnap, gotOK := l.snaps[name]
		wantSnap, wantOK := m.snaps[name]
		if gotOK != wantOK {
			t.Fatalf("snapshot %q presence=%v, want=%v", name, gotOK, wantOK)
		}
		if !gotOK {
			continue
		}
		if gotSnap.t != wantSnap.t || gotSnap.hold != wantSnap.hold {
			t.Fatalf("snapshot %q=(t:%d hold:%d), want=(t:%d hold:%d)",
				name, gotSnap.t, gotSnap.hold, wantSnap.t, wantSnap.hold)
		}
		gotRef, err := l.Referenced(name)
		if err != nil || gotRef != wantRef(name, m) {
			t.Fatalf("Referenced(%q)=(%d,%v), want=%d", name, gotRef, err, wantRef(name, m))
		}
		gotUnique, err := l.Unique(name)
		wantUnique, _ := m.unique(name)
		if err != nil || gotUnique != wantUnique {
			t.Fatalf("Unique(%q)=(%d,%v), want=%d", name, gotUnique, err, wantUnique)
		}
	}
}

func wantRef(name string, m *oracle) int64 {
	value, _ := m.referenced(name)
	return value
}

func TestRandomizedOracle(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run("seed", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			l, err := New(1000)
			if err != nil {
				t.Fatal(err)
			}
			m := newOracle(1000)
			for step := 0; step < 40; step++ {
				op := generateOp(rng, m, names)
				gotID, gotErr := executeLedger(l, op)
				wantID, wantErr := m.execute(op)
				t.Logf("input seed=%d step=%d kind=%q id=%d name=%q; output id=%d error=%v; oracle id=%d error=%v; basis cur=%d used=%d live=%d",
					seed, step, op.kind, op.i, op.name, gotID, gotErr, wantID, wantErr,
					m.cur, m.used(), m.liveBytes())
				if !sameError(gotErr, wantErr) || gotID != wantID {
					t.Fatalf("result mismatch got=(%d,%v), want=(%d,%v)", gotID, gotErr, wantID, wantErr)
				}
				compareStates(t, l, m, names)
				assertInvariants(t, l, m, names)
			}
		})
	}
}

func generateOp(rng *rand.Rand, m *oracle, names []string) testOp {
	switch rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27:
		size := int64(1 + rng.Intn(int(m.capacity+8)))
		switch rng.Intn(8) {
		case 0:
			size = 0
		case 1:
			size = 1<<40 + 1
		case 2:
			size = 1 << 40
		case 3:
			size = m.capacity - m.used() + 1
		case 4:
			size = m.capacity - m.used()
		}
		return testOp{kind: "alloc", i: size}
	case 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38:
		return testOp{kind: "free", i: generateFreeID(rng, m)}
	case 39, 40, 41, 42, 43, 44, 45, 46, 47, 48:
		name := names[rng.Intn(len(names))]
		if rng.Intn(12) == 0 {
			name = ""
		}
		return testOp{kind: "snapshot", name: name}
	case 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65:
		return testOp{kind: "destroy", name: existingOrRandomName(rng, m, names)}
	case 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77:
		return testOp{kind: "hold", name: existingOrRandomName(rng, m, names)}
	case 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88:
		return testOp{kind: "release", name: existingOrRandomName(rng, m, names)}
	default:
		return testOp{kind: "rollback", name: existingOrRandomName(rng, m, names)}
	}
}

func generateFreeID(rng *rand.Rand, m *oracle) int64 {
	switch rng.Intn(8) {
	case 0:
		return 0
	case 1:
		return m.nextID
	case 2:
		return m.nextID + 1
	case 3:
		for _, blk := range m.blocks {
			if blk.discarded {
				return blk.id
			}
		}
	case 4:
		for _, blk := range m.blocks {
			if !blk.discarded && blk.d != infinityTxn {
				return blk.id
			}
		}
	}
	if m.nextID == 1 {
		return 1
	}
	return int64(1 + rng.Intn(int(m.nextID-1)))
}

func existingOrRandomName(rng *rand.Rand, m *oracle, names []string) string {
	if len(m.snaps) > 0 && rng.Intn(4) != 0 {
		for name := range m.snaps {
			return name
		}
	}
	return names[rng.Intn(len(names))]
}

func assertInvariants(t *testing.T, l *Ledger, m *oracle, names []string) {
	t.Helper()
	used := l.Used()
	if used != m.used() || used > l.Cap() {
		t.Fatalf("invariant Used=%d oracle=%d cap=%d", used, m.used(), l.Cap())
	}
	uniqueSum := int64(0)
	for _, name := range names {
		value, ok := m.unique(name)
		if ok {
			uniqueSum += value
		}
	}
	if uniqueSum > used-m.liveBytes() {
		t.Fatalf("unique sum=%d exceeds dead-held=%d", uniqueSum, used-m.liveBytes())
	}
}

func sameError(got, want error) bool {
	if got == want {
		return true
	}
	return errors.Is(got, want)
}

func executeLedger(l *Ledger, op testOp) (int64, error) {
	switch op.kind {
	case "alloc":
		return l.Alloc(op.i)
	case "free":
		return 0, l.Free(op.i)
	case "snapshot":
		return 0, l.Snapshot(op.name)
	case "destroy":
		return 0, l.Destroy(op.name)
	case "hold":
		return 0, l.Hold(op.name)
	case "release":
		return 0, l.Release(op.name)
	case "rollback":
		return 0, l.Rollback(op.name)
	default:
		panic("unknown operation")
	}
}

func (m *oracle) execute(op testOp) (int64, error) {
	switch op.kind {
	case "alloc":
		if op.i < 1 || op.i > 1<<40 {
			return 0, ErrInvalidArgument
		}
		if m.used()+op.i > m.capacity {
			return 0, ErrOutOfSpace
		}
		id := m.nextID
		m.blocks[id] = oracleBlock{id: id, size: op.i, b: m.cur, d: infinityTxn}
		m.nextID++
		return id, nil
	case "free":
		if op.i < 1 {
			return 0, ErrInvalidArgument
		}
		blk, ok := m.blocks[op.i]
		if !ok {
			if op.i >= m.nextID {
				return 0, ErrBlockNotFound
			}
			return 0, ErrBlockDiscarded
		}
		if blk.discarded {
			return 0, ErrBlockDiscarded
		}
		if blk.d != infinityTxn {
			return 0, ErrBlockDead
		}
		blk.d = m.cur
		m.blocks[op.i] = blk
		return 0, nil
	case "snapshot":
		if op.name == "" {
			return 0, ErrInvalidArgument
		}
		if _, ok := m.snaps[op.name]; ok {
			return 0, ErrSnapshotExists
		}
		m.snaps[op.name] = oracleSnapshot{name: op.name, t: m.cur}
		m.cur++
		return 0, nil
	case "destroy":
		snap, ok := m.snaps[op.name]
		if !ok {
			return 0, ErrSnapshotMissing
		}
		if snap.hold > 0 {
			return 0, ErrHeld
		}
		delete(m.snaps, op.name)
		return 0, nil
	case "hold":
		snap, ok := m.snaps[op.name]
		if !ok {
			return 0, ErrSnapshotMissing
		}
		snap.hold++
		m.snaps[op.name] = snap
		return 0, nil
	case "release":
		snap, ok := m.snaps[op.name]
		if !ok {
			return 0, ErrSnapshotMissing
		}
		if snap.hold == 0 {
			return 0, ErrNotHeld
		}
		snap.hold--
		m.snaps[op.name] = snap
		return 0, nil
	case "rollback":
		target, ok := m.snaps[op.name]
		if !ok {
			return 0, ErrSnapshotMissing
		}
		for _, snap := range m.snaps {
			if snap.t > target.t && snap.hold > 0 {
				return 0, ErrHeld
			}
		}
		for name, snap := range m.snaps {
			if snap.t > target.t {
				delete(m.snaps, name)
			}
		}
		for id, blk := range m.blocks {
			if blk.b > target.t {
				blk.discarded = true
				m.blocks[id] = blk
				continue
			}
			if blk.d > target.t {
				blk.d = infinityTxn
				m.blocks[id] = blk
			}
		}
		return 0, nil
	default:
		panic("unknown operation")
	}
}
