package booking

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naive is an independent, deliberately simple implementation of the same
// rules: loads are recomputed from scratch per subset and every affected
// subset is enumerated explicitly.
type naive struct {
	c      int
	full   int
	s      []int64
	rho    int64
	booked map[string]Contract
	wait   []Contract
	checks int64
}

func newNaive(c int, s []int64, rho int64) *naive {
	return &naive{c: c, full: 1 << c, s: s, rho: rho, booked: map[string]Contract{}}
}

func (n *naive) cap(t int) int64 {
	var sum int64
	for j := 0; j < n.c; j++ {
		if t&(1<<j) != 0 {
			sum += n.s[j]
		}
	}
	return n.rho * sum / 100
}

func (n *naive) load(t int) int64 {
	var sum int64
	for _, ct := range n.booked {
		if t&ct.Mask == ct.Mask {
			sum += ct.Qty
		}
	}
	return sum
}

func (n *naive) fitsAdd(mask int, qty int64) bool {
	ok := true
	for t := 1; t < n.full; t++ {
		if t&mask != mask {
			continue
		}
		n.checks++
		if n.load(t)+qty > n.cap(t) {
			ok = false
		}
	}
	return ok
}

func (n *naive) fitsRetarget(m1, m2 int, qty int64) bool {
	ok := true
	for t := 1; t < n.full; t++ {
		if t&m2 != m2 || t&m1 == m1 {
			continue
		}
		n.checks++
		if n.load(t)+qty > n.cap(t) {
			ok = false
		}
	}
	return ok
}

func (n *naive) promote() {
	kept := n.wait[:0]
	for _, ct := range n.wait {
		if n.fitsAdd(ct.Mask, ct.Qty) {
			n.booked[ct.ID] = ct
			continue
		}
		kept = append(kept, ct)
	}
	n.wait = kept
}

func (n *naive) inWait(id string) bool {
	for _, ct := range n.wait {
		if ct.ID == id {
			return true
		}
	}
	return false
}

func (n *naive) book(id string, mask int, qty int64) error {
	if id == "" || mask < 1 || mask >= n.full || qty < 1 || qty > maxQty {
		return ErrInvalidParam
	}
	if _, ok := n.booked[id]; ok || n.inWait(id) {
		return ErrDuplicateID
	}
	if qty > n.cap(mask) {
		return ErrNeverBookable
	}
	if !n.fitsAdd(mask, qty) {
		n.wait = append(n.wait, Contract{ID: id, Mask: mask, Qty: qty})
		return nil
	}
	n.booked[id] = Contract{ID: id, Mask: mask, Qty: qty}
	return nil
}

func (n *naive) cancel(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if ct, ok := n.booked[id]; ok {
		delete(n.booked, id)
		_ = ct
		n.promote()
		return nil
	}
	for i, ct := range n.wait {
		if ct.ID == id {
			n.wait = append(n.wait[:i], n.wait[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (n *naive) resize(id string, q2 int64) error {
	if id == "" || q2 < 1 || q2 > maxQty {
		return ErrInvalidParam
	}
	ct, ok := n.booked[id]
	if !ok {
		if n.inWait(id) {
			return ErrNotBooked
		}
		return ErrNotFound
	}
	switch {
	case q2 == ct.Qty:
		return nil
	case q2 < ct.Qty:
		ct.Qty = q2
		n.booked[id] = ct
		n.promote()
		return nil
	default:
		if !n.fitsAdd(ct.Mask, q2-ct.Qty) {
			return ErrInfeasible
		}
		ct.Qty = q2
		n.booked[id] = ct
		return nil
	}
}

func (n *naive) retarget(id string, mask2 int) error {
	if id == "" || mask2 < 1 || mask2 >= n.full {
		return ErrInvalidParam
	}
	ct, ok := n.booked[id]
	if !ok {
		if n.inWait(id) {
			return ErrNotBooked
		}
		return ErrNotFound
	}
	if mask2 == ct.Mask {
		return nil
	}
	if !n.fitsRetarget(ct.Mask, mask2, ct.Qty) {
		return ErrInfeasible
	}
	ct.Mask = mask2
	n.booked[id] = ct
	n.promote()
	return nil
}

// random op sequences against the naive simulation.

func sameContracts(a, b []Contract) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func runRandomSequence(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	c := 1 + rng.Intn(4)
	s := make([]int64, c)
	for j := range s {
		s[j] = int64(rng.Intn(301))
	}
	rho := int64(50 + rng.Intn(251))
	full := 1 << c

	b, err := New(c, s, rho)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	n := newNaive(c, s, rho)

	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	randMask := func() int { return 1 + rng.Intn(full-1) }
	randQty := func() int64 {
		if rng.Intn(10) == 0 {
			return int64(1 + rng.Intn(2000)) // sometimes far beyond any cap
		}
		return int64(1 + rng.Intn(400))
	}

	nOps := 40
	for step := 0; step < nOps; step++ {
		id := ids[rng.Intn(len(ids))]
		var got, want error
		var desc string
		switch rng.Intn(4) {
		case 0, 1:
			mask, qty := randMask(), randQty()
			got = b.Book(id, mask, qty)
			want = n.book(id, mask, qty)
			desc = fmt.Sprintf("Book(%s, %0*b, %d)", id, c, mask, qty)
		case 2:
			got = b.Cancel(id)
			want = n.cancel(id)
			desc = fmt.Sprintf("Cancel(%s)", id)
		case 3:
			if rng.Intn(2) == 0 {
				q2 := randQty()
				got = b.Resize(id, q2)
				want = n.resize(id, q2)
				desc = fmt.Sprintf("Resize(%s, %d)", id, q2)
			} else {
				m2 := randMask()
				got = b.Retarget(id, m2)
				want = n.retarget(id, m2)
				desc = fmt.Sprintf("Retarget(%s, %0*b)", id, c, m2)
			}
		}
		t.Logf("seed=%d step=%d %s => got=%v want=%v", seed, step, desc, got, want)
		if !errors.Is(got, want) {
			t.Fatalf("seed=%d step=%d %s: got %v, want %v", seed, step, desc, got, want)
		}

		gotBooked := b.BookedContracts()
		wantBooked := make([]Contract, 0, len(n.booked))
		for _, ct := range n.booked {
			wantBooked = append(wantBooked, ct)
		}
		sort.Slice(wantBooked, func(i, j int) bool { return wantBooked[i].ID < wantBooked[j].ID })
		if !reflect.DeepEqual(gotBooked, wantBooked) {
			t.Fatalf("seed=%d step=%d %s: booked %v, want %v", seed, step, desc, gotBooked, wantBooked)
		}
		if gotWait := b.WaitlistContracts(); !sameContracts(gotWait, n.wait) {
			t.Fatalf("seed=%d step=%d %s: waitlist %v, want %v", seed, step, desc, gotWait, n.wait)
		}
		if gotChecks := b.SubsetChecks(); gotChecks != n.checks {
			t.Fatalf("seed=%d step=%d %s: subset checks %d, want %d", seed, step, desc, gotChecks, n.checks)
		}
		t.Logf("seed=%d step=%d state: booked=%v wait=%v checks=%d",
			seed, step, b.BookedContracts(), b.WaitlistContracts(), b.SubsetChecks())
	}
	t.Logf("seed=%d done: C=%d s=%v rho=%d booked=%v wait=%v checks=%d",
		seed, c, s, rho, b.BookedContracts(), b.WaitlistContracts(), b.SubsetChecks())
}

func TestRandomSequencesVsNaive(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		runRandomSequence(t, seed)
	}
}

// The same operation sequence replayed on a fresh book yields identical
// booked, waitlist and promotion results.
func TestReplayDeterminism(t *testing.T) {
	c := 3
	s := []int64{100, 50, 80}
	rho := int64(90)
	full := 1 << c
	ids := []string{"a", "b", "c", "d", "e"}

	run := func() ([]Contract, []Contract, int64) {
		b := mustNew(t, c, s, rho)
		r := rand.New(rand.NewSource(42))
		for i := 0; i < 60; i++ {
			id := ids[r.Intn(len(ids))]
			switch r.Intn(4) {
			case 0, 1:
				_ = b.Book(id, 1+r.Intn(full-1), int64(1+r.Intn(200)))
			case 2:
				_ = b.Cancel(id)
			case 3:
				if r.Intn(2) == 0 {
					_ = b.Resize(id, int64(1+r.Intn(200)))
				} else {
					_ = b.Retarget(id, 1+r.Intn(full-1))
				}
			}
		}
		return b.BookedContracts(), b.WaitlistContracts(), b.SubsetChecks()
	}
	b1, w1, c1 := run()
	b2, w2, c2 := run()
	if !reflect.DeepEqual(b1, b2) || !reflect.DeepEqual(w1, w2) || c1 != c2 {
		t.Fatalf("replay diverged: %v/%v/%d vs %v/%v/%d", b1, w1, c1, b2, w2, c2)
	}
}

// Concurrent operations must be linearizable: after any concurrent run the
// booked set is feasible and no waitlisted contract fits the booked set.
func TestConcurrentOpsKeepInvariants(t *testing.T) {
	c := 3
	s := []int64{100, 60, 90}
	rho := int64(120)
	b := mustNew(t, c, s, rho)
	full := 1 << c

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				id := fmt.Sprintf("g%d-%d", g, rng.Intn(6))
				switch rng.Intn(4) {
				case 0, 1:
					_ = b.Book(id, 1+rng.Intn(full-1), int64(1+rng.Intn(120)))
				case 2:
					_ = b.Cancel(id)
				case 3:
					if rng.Intn(2) == 0 {
						_ = b.Resize(id, int64(1+rng.Intn(120)))
					} else {
						_ = b.Retarget(id, 1+rng.Intn(full-1))
					}
				}
			}
		}(g)
	}
	wg.Wait()

	booked := b.BookedContracts()
	loads := make([]int64, full)
	for _, ct := range booked {
		for tt := 1; tt < full; tt++ {
			if tt&ct.Mask == ct.Mask {
				loads[tt] += ct.Qty
			}
		}
	}
	caps := make([]int64, full)
	for tt := 1; tt < full; tt++ {
		var sum int64
		for j := 0; j < c; j++ {
			if tt&(1<<j) != 0 {
				sum += s[j]
			}
		}
		caps[tt] = rho * sum / 100
		if loads[tt] > caps[tt] {
			t.Fatalf("booked set infeasible on subset %0*b: load %d > cap %d", c, tt, loads[tt], caps[tt])
		}
	}
	for _, ct := range b.WaitlistContracts() {
		fits := true
		for tt := 1; tt < full; tt++ {
			if tt&ct.Mask == ct.Mask && loads[tt]+ct.Qty > caps[tt] {
				fits = false
			}
		}
		if fits {
			t.Fatalf("waitlisted %+v fits the booked set but was not promoted", ct)
		}
	}
}
