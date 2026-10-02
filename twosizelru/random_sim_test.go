package twosizelru_test

import (
	"math/rand"
	"testing"

	"ontology/twosizelru"
)

type testOp struct {
	kind string
	page int
	now  int64
}

func TestRandomSequencesAgainstReference(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run("seed", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ops := generateOps(rng)
			t.Logf("seed=%d ops=%#v", seed, ops)

			capacity := 4 + rng.Intn(12)
			oldPercent := 5 + rng.Intn(91)
			tolerance := rng.Intn(3)
			stay := int64(rng.Intn(15))
			manager := newManager(t, capacity, oldPercent, tolerance, stay)
			reference := newReference(capacity, oldPercent, tolerance, stay)
			for _, op := range ops {
				playReference(t, manager, reference, op)
			}
		})
	}
}

func generateOps(rng *rand.Rand) []testOp {
	ops := make([]testOp, 0, 40)
	now := int64(0)
	for i := 0; i < 40; i++ {
		page := rng.Intn(9)
		if rng.Intn(25) == 0 {
			page = 1_000_001
		}
		kind := []string{"access", "prefetch", "pin", "unpin"}[rng.Intn(4)]
		if kind == "access" || kind == "prefetch" {
			if rng.Intn(6) == 0 && now > 0 {
				now--
			} else if rng.Intn(3) == 0 {
				now += int64(rng.Intn(20))
			}
		}
		ops = append(ops, testOp{kind: kind, page: page, now: now})
	}
	return ops
}

type reference struct {
	capacity      int
	oldPercent    int
	tolerance     int
	promotionStay int64
	maxNow        int64
	young         []int
	old           []int
	first         map[int]*int64
	pins          map[int]int
}

func newReference(capacity int, oldPercent int, tolerance int, stay int64) *reference {
	return &reference{
		capacity:      capacity,
		oldPercent:    oldPercent,
		tolerance:     tolerance,
		promotionStay: stay,
		first:         make(map[int]*int64),
		pins:          make(map[int]int),
	}
}

func playReference(t *testing.T, manager *twosizelru.Manager, ref *reference, op testOp) {
	t.Helper()

	var got twosizelru.Result
	var gotErr error
	var wantErr twosizelru.RejectReason
	var wantResult twosizelru.Result

	switch op.kind {
	case "access":
		got, gotErr = manager.Access(op.page, op.now)
		wantResult, wantErr = ref.access(op.page, op.now)
	case "prefetch":
		got, gotErr = manager.Prefetch(op.page, op.now)
		wantResult, wantErr = ref.prefetch(op.page, op.now)
	case "pin":
		gotErr = manager.Pin(op.page)
		wantErr = ref.pin(op.page)
	case "unpin":
		gotErr = manager.Unpin(op.page)
		wantErr = ref.unpin(op.page)
	default:
		t.Fatalf("unknown op: %#v", op)
	}

	if !sameReject(gotErr, wantErr) {
		t.Fatalf("op=%#v error=%v want=%v", op, gotErr, wantErr)
	}
	if got != wantResult {
		t.Fatalf("op=%#v result=%+v want=%+v", op, got, wantResult)
	}

	young, old := manager.Lists()
	if !equalSlices(young, ref.young) || !equalSlices(old, ref.old) {
		t.Fatalf("op=%#v lists=Y%v O%v want=Y%v O%v", op, young, old, ref.young, ref.old)
	}
	t.Logf("op=%#v result=%+v reject=%q after=Y%v O%v basis=serial_reference", op, got, wantErr, young, old)
}

func (r *reference) access(page int, now int64) (twosizelru.Result, twosizelru.RejectReason) {
	if page < 0 || page > 1_000_000 || now < 0 || now > 1_000_000_000_000_000 {
		return twosizelru.Result{}, twosizelru.RejectInvalidArgument
	}
	if now < r.maxNow {
		return twosizelru.Result{}, twosizelru.RejectClockRollback
	}

	if indexIn(r.young, page) >= 0 {
		r.moveToYoungHead(page)
		r.maxNow = now
		return twosizelru.Result{Hit: true}, ""
	}

	if indexIn(r.old, page) >= 0 {
		first := r.first[page]
		if first == nil {
			value := now
			r.first[page] = &value
		} else if now-*first >= r.promotionStay {
			r.old = removeInt(r.old, page)
			r.young = append([]int{page}, r.young...)
			r.rebalance()
		}
		r.maxNow = now
		return twosizelru.Result{Hit: true}, ""
	}

	result, reason := r.readMissing(page, &now)
	if reason == "" {
		r.maxNow = now
	}
	return result, reason
}

func (r *reference) prefetch(page int, now int64) (twosizelru.Result, twosizelru.RejectReason) {
	if page < 0 || page > 1_000_000 || now < 0 || now > 1_000_000_000_000_000 {
		return twosizelru.Result{}, twosizelru.RejectInvalidArgument
	}
	if now < r.maxNow {
		return twosizelru.Result{}, twosizelru.RejectClockRollback
	}

	if indexIn(r.young, page) >= 0 || indexIn(r.old, page) >= 0 {
		r.maxNow = now
		return twosizelru.Result{Hit: true}, ""
	}

	result, reason := r.readMissing(page, nil)
	if reason == "" {
		r.maxNow = now
	}
	return result, reason
}

func (r *reference) pin(page int) twosizelru.RejectReason {
	if page < 0 || page > 1_000_000 {
		return twosizelru.RejectInvalidArgument
	}
	if indexIn(r.young, page) < 0 && indexIn(r.old, page) < 0 {
		return twosizelru.RejectPageNotFound
	}
	r.pins[page]++
	return ""
}

func (r *reference) unpin(page int) twosizelru.RejectReason {
	if page < 0 || page > 1_000_000 {
		return twosizelru.RejectInvalidArgument
	}
	if indexIn(r.young, page) < 0 && indexIn(r.old, page) < 0 {
		return twosizelru.RejectPageNotFound
	}
	if r.pins[page] == 0 {
		return twosizelru.RejectUnpinUnderflow
	}
	r.pins[page]--
	return ""
}

func (r *reference) readMissing(page int, first *int64) (twosizelru.Result, twosizelru.RejectReason) {
	result := twosizelru.Result{Read: true}
	if len(r.young)+len(r.old) == r.capacity {
		victim := -1
		for i := len(r.old) - 1; i >= 0; i-- {
			if r.pins[r.old[i]] == 0 {
				victim = r.old[i]
				break
			}
		}
		if victim == -1 {
			for i := len(r.young) - 1; i >= 0; i-- {
				if r.pins[r.young[i]] == 0 {
					victim = r.young[i]
					break
				}
			}
		}
		if victim == -1 {
			return twosizelru.Result{}, twosizelru.RejectAllPinned
		}

		r.old = removeInt(r.old, victim)
		r.young = removeInt(r.young, victim)
		delete(r.first, victim)
		delete(r.pins, victim)
		result.Evicted = true
		result.EvictedPage = victim
	}

	delete(r.first, page)
	r.first[page] = first
	r.old = append([]int{page}, r.old...)
	r.rebalance()
	return result, ""
}

func (r *reference) rebalance() {
	length := len(r.young) + len(r.old)
	target := length * r.oldPercent / 100

	for len(r.old) < target && len(r.young) > 0 {
		page := r.young[len(r.young)-1]
		r.young = r.young[:len(r.young)-1]
		r.old = append([]int{page}, r.old...)
	}

	for len(r.old) > target+r.tolerance {
		page := r.old[0]
		r.old = append([]int(nil), r.old[1:]...)
		r.young = append(r.young, page)
	}
}

func (r *reference) moveToYoungHead(page int) {
	r.young = removeInt(r.young, page)
	r.young = append([]int{page}, r.young...)
}

func indexIn(values []int, page int) int {
	for i, value := range values {
		if value == page {
			return i
		}
	}
	return -1
}

func removeInt(values []int, page int) []int {
	index := indexIn(values, page)
	if index < 0 {
		return values
	}
	return append(values[:index], values[index+1:]...)
}

func sameReject(actual error, want twosizelru.RejectReason) bool {
	if want == "" {
		return actual == nil
	}
	return actual != nil && actual.Error() == want.Error()
}
