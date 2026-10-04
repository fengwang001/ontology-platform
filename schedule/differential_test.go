package schedule_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/schedule"
)

// opKind 枚举随机操作种类。
type opKind int

const (
	kBook opKind = iota
	kCancel
	kEmergency
	kInvalidBook
	kDupBook
)

type genOp struct {
	kind    opKind
	now     int64
	id      string
	room    string
	start   int64
	dur     int64
	surgeon string
	needs   map[string]int
}

// genSequenceWith 基于给定房间与设备类型生成一条操作序列。
func genSequenceWith(rng *rand.Rand, roomNames, typeNames []string) []genOp {
	var ops []genOp

	now := int64(0)

	seq := 0
	nextID := func() string { seq++; return fmt.Sprintf("S%04d", seq) }
	idPool := []string{}
	emIDPool := []string{}

	mkNeeds := func() map[string]int {
		if len(typeNames) == 0 || rng.Intn(2) == 0 {
			return nil
		}
		k := 1 + rng.Intn(min(3, len(typeNames)))
		rng.Shuffle(len(typeNames), func(i, j int) {
			typeNames[i], typeNames[j] = typeNames[j], typeNames[i]
		})
		m := map[string]int{}
		for _, t := range typeNames[:k] {
			m[t] = 1
		}
		return m
	}

	length := 30 + rng.Intn(90)
	for i := 0; i < length; i++ {
		// 时钟：以较大概率递增，偶尔不变。
		if rng.Intn(5) != 0 {
			now += int64(rng.Intn(40))
		}
		room := roomNames[rng.Intn(len(roomNames))]
		surgeon := fmt.Sprintf("doc%d", rng.Intn(5))
		dur := int64(1 + rng.Intn(120))
		start := now + int64(rng.Intn(200))

		r := rng.Intn(100)
		switch {
		case r < 70:
			id := nextID()
			op := genOp{kind: kBook, now: now, id: id, room: room, start: start,
				dur: dur, surgeon: surgeon, needs: mkNeeds()}
			ops = append(ops, op)
			idPool = append(idPool, id)
		case r < 82 && len(idPool) > 0:
			id := idPool[rng.Intn(len(idPool))]
			ops = append(ops, genOp{kind: kCancel, now: now, id: id})
		case r < 97:
			id := nextID()
			op := genOp{kind: kEmergency, now: now, id: id, room: room,
				dur: int64(1 + rng.Intn(60)), surgeon: surgeon, needs: mkNeeds()}
			ops = append(ops, op)
			emIDPool = append(emIDPool, id)
		case r < 99 && len(emIDPool) > 0:
			id := emIDPool[rng.Intn(len(emIDPool))]
			ops = append(ops, genOp{kind: kDupBook, now: now, id: id, room: room,
				start: start, dur: dur, surgeon: surgeon})
		default:
			ops = append(ops, genOp{kind: kInvalidBook, now: now, id: nextID(),
				room: room, start: start, dur: 0, surgeon: surgeon})
		}
	}
	return ops
}

func setupPair(t *testing.T, rng *rand.Rand, roomNames []string) (*schedule.Scheduler, *naive, []string) {
	t.Helper()
	sc := schedule.New()
	nm := newNaive()
	for i, r := range roomNames {
		turn := []int{0, 10, 20, 30, 60, 240}[i%6]
		must(t, sc.AddRoom(0, r, turn))
		if nm.addRoom(0, r, turn) != "ok" {
			t.Fatalf("naive addRoom %s", r)
		}
	}
	var typeNames []string
	for i := 0; i < 1+rng.Intn(3); i++ {
		name := fmt.Sprintf("E%d", i+1)
		n := 1 + rng.Intn(2)
		st := []int{0, 15, 30}[rng.Intn(3)]
		must(t, sc.AddEquip(0, name, n, st))
		if nm.addEquip(0, name, n, st) != "ok" {
			t.Fatalf("naive addEquip %s", name)
		}
		typeNames = append(typeNames, name)
	}
	return sc, nm, typeNames
}

func describe(o genOp) string {
	switch o.kind {
	case kBook:
		return fmt.Sprintf("Book(now=%d id=%s room=%s start=%d dur=%d surgeon=%s needs=%v)",
			o.now, o.id, o.room, o.start, o.dur, o.surgeon, o.needs)
	case kCancel:
		return fmt.Sprintf("Cancel(now=%d id=%s)", o.now, o.id)
	case kEmergency:
		return fmt.Sprintf("Emergency(now=%d id=%s dur=%d surgeon=%s needs=%v)",
			o.now, o.id, o.dur, o.surgeon, o.needs)
	case kDupBook:
		return fmt.Sprintf("Book-DUP(now=%d id=%s room=%s start=%d dur=%d)",
			o.now, o.id, o.room, o.start, o.dur)
	default:
		return fmt.Sprintf("Book-INVALID(now=%d id=%s dur=%d)", o.now, o.id, o.dur)
	}
}

// TestRandomDifferential 1500 组随机序列与朴素模拟器逐操作对照。
func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		seed := int64(1 + g)
		rng := rand.New(rand.NewSource(seed))

		nRooms := 1 + rng.Intn(4)
		roomNames := make([]string, nRooms)
		for i := range roomNames {
			roomNames[i] = fmt.Sprintf("R%d", i+1)
		}
		sc, nm, typeNames := setupPair(t, rng, roomNames)

		ops := genSequenceWith(rng, roomNames, typeNames)
		var trace strings.Builder
		for step, o := range ops {
			fmt.Fprintf(&trace, "seed=%d step=%d %s\n", seed, step, describe(o))

			var gotCode, wantCode string
			var gotRoom string
			var gotStart int64
			var gotDis []string

			switch o.kind {
			case kBook, kDupBook, kInvalidBook:
				err := sc.Book(o.now, o.id, o.room, o.start, o.dur, o.surgeon, o.needs)
				gotCode = code(err)
				wantCode = nm.book(o.now, nSurgery{id: o.id, room: o.room, start: o.start,
					dur: o.dur, surgeon: o.surgeon, needs: o.needs})
			case kCancel:
				err := sc.Cancel(o.now, o.id)
				gotCode = code(err)
				wantCode = nm.cancel(o.now, o.id)
			case kEmergency:
				res, err := sc.Emergency(o.now, o.id, o.dur, o.surgeon, o.needs)
				gotCode = code(err)
				wr := nm.emergency(o.now, nSurgery{id: o.id, dur: o.dur, surgeon: o.surgeon, needs: o.needs})
				wantCode = wr.code
				if err == nil {
					gotRoom, gotStart, gotDis = res.Room, res.Start, res.Displaced
				}
				if gotCode == "ok" {
					if gotRoom != wr.room || gotStart != wr.start ||
						!sliceEq(gotDis, wr.displaced) {
						t.Fatalf("seed=%d step=%d emergency mismatch:\n%sgot=(%s,%d,%v) want=(%s,%d,%v)",
							seed, step, trace.String(), gotRoom, gotStart, gotDis,
							wr.room, wr.start, wr.displaced)
					}
				}
			}
			if gotCode != wantCode {
				t.Fatalf("seed=%d step=%d code mismatch:\n%sgot=%s want=%s",
					seed, step, trace.String(), gotCode, wantCode)
			}
			if msg := nm.invariants(); msg != "" {
				t.Fatalf("seed=%d step=%d %s\ntrace:\n%s", seed, step, msg, trace.String())
			}
		}
		if g%200 == 0 {
			t.Logf("group %d ok (seed=%d, ops=%d)", g, seed, len(ops))
		}
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
