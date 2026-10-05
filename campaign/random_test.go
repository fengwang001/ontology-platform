package campaign

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naive is an independent, deliberately simple implementation of the
// campaign rules: no heaps, no slot structure, every operation works
// on a deep copy so a rejected operation changes nothing by
// construction. The random test cross-checks the real implementation
// against it operation by operation.
type naive struct {
	T, C, R, F int
	B, D       int64
	M          []int // sorted
	devs       map[string]*nDev
	aborted    bool
	failed     int
	tok        int
	clock      int64
	inflight   int
}

type nDev struct {
	state    State
	ver      int
	attempts int
	readyAt  int64
	hop      int
	tok      int
	dl       int64
}

func newNaive(T int, M []int, C, R int, B, D int64, F int) *naive {
	m := append([]int(nil), M...)
	sort.Ints(m)
	return &naive{T: T, M: m, C: C, R: R, B: B, D: D, F: F, devs: map[string]*nDev{}, clock: -1}
}

func (n *naive) clone() *naive {
	w := *n
	w.devs = make(map[string]*nDev, len(n.devs))
	for k, v := range n.devs {
		d := *v
		w.devs[k] = &d
	}
	return &w
}

func (n *naive) next(v int) int {
	for _, m := range n.M {
		if m > v {
			return m
		}
	}
	return n.T
}

func (n *naive) abort() {
	n.aborted = true
	for _, d := range n.devs {
		if d.state == Pending {
			d.state = Cancelled
		}
	}
}

// settleAll settles every timeout with dl <= now, one by one in
// (dl, id) order, found by a full linear scan each round.
func (n *naive) settleAll(now int64) {
	for {
		sel := ""
		var selDL int64
		found := false
		for id, d := range n.devs {
			if d.state != InFlight || d.dl > now {
				continue
			}
			if !found || d.dl < selDL || (d.dl == selDL && id < sel) {
				sel, selDL, found = id, d.dl, true
			}
		}
		if !found {
			return
		}
		d := n.devs[sel]
		n.inflight--
		d.attempts++
		switch {
		case d.attempts >= n.R:
			d.state = Failed
			n.failed++
			if !n.aborted && n.failed >= n.F {
				n.abort()
			}
		case n.aborted:
			d.state = Cancelled
		default:
			d.state = Pending
			d.readyAt = selDL + n.B*int64(d.attempts)
		}
	}
}

func clockErr(now, clock int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalid
	}
	if now < clock {
		return ErrClockBack
	}
	return nil
}

func (n *naive) addDevice(now int64, id string, v int) error {
	if id == "" || v < 1 || v > 1_000_000 {
		return ErrInvalid
	}
	if err := clockErr(now, n.clock); err != nil {
		return err
	}
	w := n.clone()
	w.settleAll(now)
	if _, dup := w.devs[id]; dup {
		return ErrExists
	}
	d := &nDev{ver: v, readyAt: now}
	switch {
	case v >= w.T:
		d.state = Skipped
	case w.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
	}
	w.devs[id] = d
	w.clock = now
	*n = *w
	return nil
}

func (n *naive) dispatch(now int64, num int) ([]Item, error) {
	if num < 1 || num > 10_000 {
		return nil, ErrInvalid
	}
	if err := clockErr(now, n.clock); err != nil {
		return nil, err
	}
	w := n.clone()
	w.settleAll(now)
	if w.aborted {
		return nil, ErrAborted
	}
	k := min(num, w.C-w.inflight)
	var out []Item
	for len(out) < k {
		sel := ""
		var selRA int64
		found := false
		for id, d := range w.devs {
			if d.state != Pending || d.readyAt > now {
				continue
			}
			if !found || d.readyAt < selRA || (d.readyAt == selRA && id < sel) {
				sel, selRA, found = id, d.readyAt, true
			}
		}
		if !found {
			break
		}
		d := w.devs[sel]
		d.state = InFlight
		w.inflight++
		d.hop = w.next(d.ver)
		w.tok++
		d.tok = w.tok
		d.dl = now + w.D
		out = append(out, Item{ID: []byte(sel), Hop: d.hop, Tok: d.tok})
	}
	w.clock = now
	*n = *w
	return out, nil
}

func (n *naive) report(now int64, id string, tok, ver int, ok bool) error {
	if id == "" {
		return ErrInvalid
	}
	if err := clockErr(now, n.clock); err != nil {
		return err
	}
	w := n.clone()
	w.settleAll(now)
	d, found := w.devs[id]
	if !found {
		return ErrUnknown
	}
	if d.state != InFlight {
		return ErrNotInFlight
	}
	if tok != d.tok {
		return ErrStale
	}
	if ok && ver != d.hop {
		return ErrVersion
	}
	w.inflight--
	if ok {
		d.ver = ver
		d.attempts = 0
		switch {
		case ver == w.T:
			d.state = Done
		case w.aborted:
			d.state = Cancelled
		default:
			d.state = Pending
			d.readyAt = now
		}
	} else {
		d.attempts++
		switch {
		case d.attempts >= w.R:
			d.state = Failed
			w.failed++
			if !w.aborted && w.failed >= w.F {
				w.abort()
			}
		case w.aborted:
			d.state = Cancelled
		default:
			d.state = Pending
			d.readyAt = now + w.B*int64(d.attempts)
		}
	}
	w.clock = now
	*n = *w
	return nil
}

func (n *naive) dump() string {
	var b strings.Builder
	fmt.Fprintf(&b, "aborted=%v failed=%d tok=%d clock=%d inflight=%d\n",
		n.aborted, n.failed, n.tok, n.clock, n.inflight)
	ids := make([]string, 0, len(n.devs))
	for id := range n.devs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := n.devs[id]
		fmt.Fprintf(&b, "%s %s v=%d att=%d ra=%d hop=%d tok=%d dl=%d\n",
			id, d.state, d.ver, d.attempts, d.readyAt, d.hop, d.tok, d.dl)
	}
	return b.String()
}

func dumpCampaign(c *Campaign) string {
	var b strings.Builder
	fmt.Fprintf(&b, "aborted=%v failed=%d tok=%d clock=%d inflight=%d\n",
		c.aborted, c.failed, c.tok, c.clock, c.slots.InFlight())
	ids := make([]string, 0, len(c.devices))
	for id := range c.devices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := c.devices[id]
		fmt.Fprintf(&b, "%s %s v=%d att=%d ra=%d hop=%d tok=%d dl=%d\n",
			id, d.state, d.ver, d.attempts, d.readyAt, d.hop, d.tok, d.dl)
	}
	return b.String()
}

// TestRandomVsNaive runs 1500 random operation sequences through both
// the real campaign and the naive simulation and compares, after every
// single operation, the returned error, the dispatched items and the
// full state. Inputs, outputs and the verdict basis are logged.
func TestRandomVsNaive(t *testing.T) {
	const sequences = 1500
	for seed := int64(1); seed <= sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		T := 2 + rng.Intn(15)
		var M []int
		perm := rng.Perm(T - 1)
		for i := 0; i < rng.Intn(min(3, T-1)+1); i++ {
			M = append(M, perm[i]+1)
		}
		C := 1 + rng.Intn(4)
		R := 1 + rng.Intn(3)
		B := int64(1 + rng.Intn(15))
		D := int64(1 + rng.Intn(40))
		F := 1 + rng.Intn(4)

		c, err := New(T, M, C, R, B, D, F)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		n := newNaive(T, M, C, R, B, D, F)
		t.Logf("seed=%d params T=%d M=%v C=%d R=%d B=%d D=%d F=%d", seed, T, M, C, R, B, D, F)

		var now int64
		var added []string
		lastDisp := map[string]Item{}
		hist := map[string][]int{}
		wantTok := 1
		idSeq := 0

		fail := func(op int, format string, args ...any) {
			t.Helper()
			msg := fmt.Sprintf(format, args...)
			t.Fatalf("seed=%d op=%d: %s\nreal:\n%s\nnaive:\n%s", seed, op, msg, dumpCampaign(c), n.dump())
		}

		for op := 0; op < 40; op++ {
			// Recover from an out-of-range clock so later ops in the
			// sequence exercise real behavior again.
			if (now < 0 || now > 1_000_000_000_000) && rng.Intn(100) < 60 {
				now = rng.Int63n(200)
			}
			switch r := rng.Intn(100); {
			case r < 60:
				now += rng.Int63n(60)
			case r < 70:
				// unchanged
			case r < 80:
				now -= rng.Int63n(30) // may go negative: ErrInvalid or ErrClockBack
			case r < 92:
				now += 1 + rng.Int63n(300)
			case r < 96:
				now = 1_000_000_000_001
			default:
				now = -1 - rng.Int63n(10)
			}

			choice := rng.Intn(100)
			switch {
			case choice < 30: // AddDevice
				var id string
				switch r := rng.Intn(100); {
				case r < 70:
					id = fmt.Sprintf("d%d", idSeq)
					idSeq++
				case r < 90:
					if len(added) > 0 {
						id = added[rng.Intn(len(added))]
					} else {
						id = "dup"
					}
				default:
					id = ""
				}
				v := 1 + rng.Intn(T+1)
				if rng.Intn(100) < 12 {
					v = 0
				}
				if rng.Intn(100) < 8 {
					v = 1_000_001
				}
				gotErr := c.AddDevice(now, []byte(id), v)
				wantErr := n.addDevice(now, id, v)
				t.Logf("seed=%d op=%d add(now=%d id=%q v=%d) => %v (basis: naive=%v)",
					seed, op, now, id, v, gotErr, wantErr)
				if gotErr != wantErr {
					fail(op, "add err = %v, naive = %v", gotErr, wantErr)
				}
				if gotErr == nil {
					added = append(added, id)
					hist[id] = []int{v}
				}
			case choice < 60: // Dispatch
				num := 1 + rng.Intn(6)
				if rng.Intn(100) < 10 {
					num = 0
				}
				if rng.Intn(100) < 8 {
					num = 10_001
				}
				gotItems, gotErr := c.Dispatch(now, num)
				wantItems, wantErr := n.dispatch(now, num)
				t.Logf("seed=%d op=%d dispatch(now=%d n=%d) => %v err=%v (basis: naive=%v err=%v)",
					seed, op, now, num, gotItems, gotErr, wantItems, wantErr)
				if gotErr != wantErr {
					fail(op, "dispatch err = %v, naive = %v", gotErr, wantErr)
				}
				if !equalItems(gotItems, wantItems) {
					fail(op, "dispatch items = %v, naive = %v", gotItems, wantItems)
				}
				if gotErr == nil {
					for _, it := range gotItems {
						if it.Tok != wantTok {
							fail(op, "token hole: got tok %d, want %d", it.Tok, wantTok)
						}
						wantTok++
						lastDisp[string(it.ID)] = it
					}
				}
			default: // Report
				var id string
				var tok, ver int
				ok := rng.Intn(100) < 65
				switch r := rng.Intn(100); {
				case r < 55 && len(lastDisp) > 0:
					keys := make([]string, 0, len(lastDisp))
					for k := range lastDisp {
						keys = append(keys, k)
					}
					id = keys[rng.Intn(len(keys))]
					it := lastDisp[id]
					tok, ver = it.Tok, it.Hop
					if rng.Intn(100) < 25 {
						tok = it.Tok - 1 - rng.Intn(2) // stale token
					}
					if rng.Intn(100) < 25 {
						ver = it.Hop + 1 + rng.Intn(2) // wrong version
					}
				case r < 80 && len(added) > 0:
					id = added[rng.Intn(len(added))]
					tok = rng.Intn(wantTok + 2)
					ver = rng.Intn(T + 2)
				default:
					id = fmt.Sprintf("ghost%d", rng.Intn(3))
					tok = rng.Intn(3)
					ver = rng.Intn(3)
				}
				gotErr := c.Report(now, []byte(id), tok, ver, ok)
				wantErr := n.report(now, id, tok, ver, ok)
				t.Logf("seed=%d op=%d report(now=%d id=%q tok=%d ver=%d ok=%v) => %v (basis: naive=%v)",
					seed, op, now, id, tok, ver, ok, gotErr, wantErr)
				if gotErr != wantErr {
					fail(op, "report err = %v, naive = %v", gotErr, wantErr)
				}
				if gotErr == nil && ok {
					h := hist[id]
					prev := h[len(h)-1]
					if want := n.next(prev); ver != want {
						fail(op, "version path: %q jumped %d -> %d, want next hop %d", id, prev, ver, want)
					}
					hist[id] = append(h, ver)
				}
			}

			if got, want := dumpCampaign(c), n.dump(); got != want {
				fail(op, "state mismatch (basis: full state equality with naive simulation)")
			}
			checkInvariants(t, c)
		}
	}
}
