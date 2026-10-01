package dmaring

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveModel is a deliberately naive, step-by-step implementation of the
// spec: descriptors live in an ever-growing slice indexed by the unbounded
// sequence number, with no modular slot arithmetic at all. The cross-check
// test replays identical operation sequences against Ring and naiveModel
// and demands identical results.
type naiveDesc struct {
	own, first, last bool
	length, st       int
}

type naiveModel struct {
	n      int
	descs  []naiveDesc // descs[i] is the descriptor with unbounded seq i; len == prod
	prod   int
	dev    int
	reap   int
	faults map[int]bool
}

func newNaiveModel(n int) *naiveModel {
	return &naiveModel{n: n, faults: map[int]bool{}}
}

func (m *naiveModel) free() int { return m.n - (m.prod - m.reap) }

func (m *naiveModel) submit(lens []int) error {
	if len(lens) == 0 {
		return ErrEmptyLens
	}
	for _, l := range lens {
		if l <= 0 {
			return ErrNonPositiveLen
		}
	}
	if len(lens) > m.n {
		return ErrTooManySegments
	}
	if len(lens) > m.free() {
		return ErrInsufficientFree
	}
	for i, l := range lens {
		m.descs = append(m.descs, naiveDesc{
			own:    true,
			first:  i == 0,
			last:   i == len(lens)-1,
			length: l,
		})
	}
	m.prod += len(lens)
	return nil
}

func (m *naiveModel) deviceRun(k int) (int, error) {
	if k < 0 {
		return 0, ErrNegativeK
	}
	done := 0
	for done < k && m.dev < m.prod && m.descs[m.dev].own {
		if m.faults[m.dev] {
			delete(m.faults, m.dev)
			m.descs[m.dev].own = false
			m.descs[m.dev].st = 2
			last := m.descs[m.dev].last
			m.dev++
			for !last {
				m.descs[m.dev].own = false
				m.descs[m.dev].st = 3
				last = m.descs[m.dev].last
				delete(m.faults, m.dev)
				m.dev++
			}
		} else {
			m.descs[m.dev].own = false
			m.descs[m.dev].st = 1
			m.dev++
		}
		done++
	}
	return done, nil
}

func (m *naiveModel) fault(seq int) error {
	if seq < m.dev {
		return ErrFaultBeforeDev
	}
	m.faults[seq] = true
	return nil
}

func (m *naiveModel) reapPkt() (ReapResult, error) {
	if m.reap == m.prod {
		return ReapResult{}, ErrNoPacket
	}
	res := ReapResult{ErrIndex: -1}
	for seq := m.reap; ; seq++ {
		d := m.descs[seq]
		if d.own {
			return ReapResult{}, ErrPacketIncomplete
		}
		res.Segments++
		res.TotalLen += d.length
		if d.st == 2 {
			res.ErrIndex = res.Segments - 1
		}
		if d.last {
			break
		}
	}
	m.reap += res.Segments
	return res, nil
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) || errors.Is(b, a)
}

// checkInvariants verifies the global invariants on the ring and that the
// ring's slot contents match the naive model for every live sequence
// number in [reap, prod).
func checkInvariants(t *testing.T, r *Ring, m *naiveModel) {
	t.Helper()
	prod, dev, reap := r.Pointers()
	if !(reap <= dev && dev <= prod) {
		t.Fatalf("invariant reap<=dev<=prod broken: (%d,%d,%d)", prod, dev, reap)
	}
	if prod-reap > uint64(m.n) {
		t.Fatalf("invariant prod-reap<=N broken: prod-reap=%d N=%d", prod-reap, m.n)
	}
	ownCount := 0
	for _, s := range r.slots {
		if s.Own {
			ownCount++
		}
	}
	if uint64(ownCount) != prod-dev {
		t.Fatalf("OWN=1 count %d != prod-dev %d", ownCount, prod-dev)
	}
	if r.Free() != m.free() {
		t.Fatalf("Free mismatch: ring=%d model=%d", r.Free(), m.free())
	}
	for seq := reap; seq < prod; seq++ {
		s := r.slots[seq%uint64(m.n)]
		d := m.descs[seq]
		if s.Own != d.own || s.First != d.first || s.Last != d.last ||
			s.Len != d.length || s.St != d.st {
			t.Fatalf("seq %d mismatch: ring=%+v model=%+v", seq, s, d)
		}
	}
}

func TestCrossCheckWithNaiveModel(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(n) * 7919))
			r := mustRing(t, n)
			m := newNaiveModel(n)
			for step := 0; step < 3000; step++ {
				op := rng.Intn(100)
				switch {
				case op < 40: // Submit
					segs := rng.Intn(n + 3) // 0..n+2 segments
					lens := make([]int, segs)
					for i := range lens {
						lens[i] = rng.Intn(7) - 1 // -1..5, sometimes invalid
					}
					errR := r.Submit(lens)
					errM := m.submit(lens)
					t.Logf("step %d Submit(%v) -> ring=%v model=%v (free=%d)", step, lens, errR, errM, m.free())
					if !sameErr(errR, errM) {
						t.Fatalf("step %d Submit(%v): ring=%v model=%v", step, lens, errR, errM)
					}
				case op < 65: // DeviceRun
					k := rng.Intn(8) - 1 // -1..6
					doneR, errR := r.DeviceRun(k)
					doneM, errM := m.deviceRun(k)
					t.Logf("step %d DeviceRun(%d) -> ring=(%d,%v) model=(%d,%v)", step, k, doneR, errR, doneM, errM)
					if doneR != doneM || !sameErr(errR, errM) {
						t.Fatalf("step %d DeviceRun(%d): ring=(%d,%v) model=(%d,%v)", step, k, doneR, errR, doneM, errM)
					}
				case op < 80: // Fault
					_, dev, _ := r.Pointers()
					seq := int(dev) + rng.Intn(6) - 1 // sometimes < dev
					if seq < 0 {
						seq = 0
					}
					errR := r.Fault(uint64(seq))
					errM := m.fault(seq)
					t.Logf("step %d Fault(%d) -> ring=%v model=%v (dev=%d)", step, seq, errR, errM, dev)
					if !sameErr(errR, errM) {
						t.Fatalf("step %d Fault(%d): ring=%v model=%v", step, seq, errR, errM)
					}
				case op < 95: // Reap
					resR, errR := r.Reap()
					resM, errM := m.reapPkt()
					t.Logf("step %d Reap() -> ring=(%+v,%v) model=(%+v,%v)", step, resR, errR, resM, errM)
					if resR != resM || !sameErr(errR, errM) {
						t.Fatalf("step %d Reap: ring=(%+v,%v) model=(%+v,%v)", step, resR, errR, resM, errM)
					}
				default: // query only
					t.Logf("step %d query Free=%d", step, r.Free())
				}
				checkInvariants(t, r, m)
			}
		})
	}
}

// TestConcurrentAccess hammers the ring from many goroutines; with -race
// this proves the mutex discipline, and the final invariant check proves
// the state is still coherent (equivalent to some serial order).
func TestConcurrentAccess(t *testing.T) {
	r := mustRing(t, 16)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Submitters keep the ring busy, backing off on ErrInsufficientFree.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				segs := 1 + rng.Intn(4)
				lens := make([]int, segs)
				for i := range lens {
					lens[i] = 1 + rng.Intn(100)
				}
				_ = r.Submit(lens)
			}
		}(int64(g + 1))
	}
	// Device worker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(99))
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = r.DeviceRun(rng.Intn(4))
		}
	}()
	// Reaper.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = r.Reap()
		}
	}()
	// Fault registrar only registers seqs ahead of dev.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(7))
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, dev, _ := r.Pointers()
			_ = r.Fault(dev + uint64(rng.Intn(8)))
		}
	}()

	// Let them race, checking invariants concurrently from here.
	for i := 0; i < 2000; i++ {
		prod, dev, reap := r.Pointers()
		if !(reap <= dev && dev <= prod) {
			t.Fatalf("invariant reap<=dev<=prod broken: (%d,%d,%d)", prod, dev, reap)
		}
		if prod-reap > 16 {
			t.Fatalf("invariant prod-reap<=N broken: %d", prod-reap)
		}
		_ = r.Free()
	}
	close(stop)
	wg.Wait()

	// Final consistency: OWN=1 count == prod-dev.
	prod, dev, _ := r.Pointers()
	ownCount := 0
	for _, s := range r.slots {
		if s.Own {
			ownCount++
		}
	}
	if uint64(ownCount) != prod-dev {
		t.Fatalf("OWN=1 count %d != prod-dev %d", ownCount, prod-dev)
	}
}
