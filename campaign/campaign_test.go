package campaign

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// step is one scripted operation. op is "add", "dispatch" or "report".
type step struct {
	op      string
	now     int64
	id      string
	v       int
	n       int
	tok     int
	ver     int
	ok      bool
	wantErr error
	want    []Item // expected dispatch result
}

type scenario struct {
	name  string
	T     int
	M     []int
	C, R  int
	B, D  int64
	F     int
	steps []step
	final func(t *testing.T, c *Campaign)
}

func it(id string, hop, tok int) Item { return Item{ID: []byte(id), Hop: hop, Tok: tok} }

func runScenario(t *testing.T, sc scenario) {
	t.Helper()
	c, err := New(sc.T, sc.M, sc.C, sc.R, sc.B, sc.D, sc.F)
	if err != nil {
		t.Fatalf("%s: New: %v", sc.name, err)
	}
	for i, st := range sc.steps {
		ctx := fmt.Sprintf("%s step %d (%s)", sc.name, i, st.op)
		switch st.op {
		case "add":
			if err := c.AddDevice(st.now, []byte(st.id), st.v); !errors.Is(err, st.wantErr) {
				t.Fatalf("%s: err = %v, want %v", ctx, err, st.wantErr)
			}
		case "report":
			if err := c.Report(st.now, []byte(st.id), st.tok, st.ver, st.ok); !errors.Is(err, st.wantErr) {
				t.Fatalf("%s: err = %v, want %v", ctx, err, st.wantErr)
			}
		case "dispatch":
			got, err := c.Dispatch(st.now, st.n)
			if !errors.Is(err, st.wantErr) {
				t.Fatalf("%s: err = %v, want %v", ctx, err, st.wantErr)
			}
			if st.wantErr == nil && !equalItems(got, st.want) {
				t.Fatalf("%s: items = %v, want %v", ctx, got, st.want)
			}
		default:
			t.Fatalf("%s: bad op %q", ctx, st.op)
		}
	}
	if sc.final != nil {
		sc.final(t, c)
	}
}

func equalItems(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i].ID) != string(b[i].ID) || a[i].Hop != b[i].Hop || a[i].Tok != b[i].Tok {
			return false
		}
	}
	return true
}

func checkDev(t *testing.T, c *Campaign, id string, state State, ver, attempts int, readyAt int64) {
	t.Helper()
	d, ok := c.devices[id]
	if !ok {
		t.Errorf("device %q missing", id)
		return
	}
	if d.state != state || d.ver != ver || d.attempts != attempts || d.readyAt != readyAt {
		t.Errorf("device %q = {%s v=%d att=%d ra=%d}, want {%s v=%d att=%d ra=%d}",
			id, d.state, d.ver, d.attempts, d.readyAt, state, ver, attempts, readyAt)
	}
}

func TestScenarios(t *testing.T) {
	scenarios := []scenario{
		{
			// The worked example from the specification.
			name: "worked example",
			T:    6, M: []int{3, 5}, C: 2, R: 2, B: 10, D: 100, F: 2,
			steps: []step{
				{op: "add", now: 0, id: "a", v: 1},
				{op: "add", now: 0, id: "b", v: 3},
				{op: "add", now: 0, id: "c", v: 5},
				{op: "add", now: 0, id: "d", v: 6}, // v >= T: Skipped
				{op: "dispatch", now: 0, n: 5, want: []Item{it("a", 3, 1), it("b", 5, 2)}},
				{op: "report", now: 10, id: "a", tok: 1, ver: 3, ok: true},
				{op: "dispatch", now: 10, n: 5, want: []Item{it("c", 6, 3)}},
				{op: "dispatch", now: 100, n: 1, want: []Item{it("a", 5, 4)}},
				// c times out at dl=110 inside this report: rejected and rolled back.
				{op: "report", now: 110, id: "c", tok: 3, ver: 6, ok: true, wantErr: ErrNotInFlight},
				{op: "dispatch", now: 110, n: 1, want: []Item{it("b", 5, 5)}},
			},
			final: func(t *testing.T, c *Campaign) {
				checkDev(t, c, "a", InFlight, 3, 0, 10)
				checkDev(t, c, "b", InFlight, 3, 1, 110)
				checkDev(t, c, "c", Pending, 5, 1, 120)
				checkDev(t, c, "d", Skipped, 6, 0, 0)
				if c.tok != 5 || c.failed != 0 || c.aborted {
					t.Errorf("tok=%d failed=%d aborted=%v", c.tok, c.failed, c.aborted)
				}
			},
		},
		{
			// now == dl times out exactly; the backoff of a timeout
			// failure is counted from dl, not from the call time; a
			// rejected report rolls the settlement back.
			name: "timeout exact dl and dl-based backoff",
			T:    10, M: nil, C: 2, R: 3, B: 7, D: 100, F: 5,
			steps: []step{
				{op: "add", now: 0, id: "a", v: 1},
				{op: "dispatch", now: 0, n: 1, want: []Item{it("a", 10, 1)}},
				// now == dl: a times out inside the report, so the
				// report is rejected and the settlement rolled back.
				{op: "report", now: 100, id: "a", tok: 1, ver: 10, ok: true, wantErr: ErrNotInFlight},
				// still InFlight: the rejected report changed nothing.
				{op: "report", now: 100, id: "a", tok: 1, ver: 10, ok: true, wantErr: ErrNotInFlight},
				// accepted op lands the timeout: readyAt = dl + B*1 = 107,
				// not 5000 + 7.
				{op: "add", now: 5000, id: "b", v: 1},
			},
			final: func(t *testing.T, c *Campaign) {
				checkDev(t, c, "a", Pending, 1, 1, 107)
				checkDev(t, c, "b", Pending, 1, 0, 5000)
			},
		},
		{
			// Circuit breaker: in-flight devices keep running after the
			// abort and reach their three possible outcomes.
			name: "abort in-flight outcomes",
			T:    6, M: []int{3, 5}, C: 4, R: 2, B: 10, D: 100, F: 1,
			steps: []step{
				{op: "add", now: 0, id: "a", v: 1},
				{op: "add", now: 0, id: "b", v: 3},
				{op: "add", now: 0, id: "c", v: 5},
				{op: "add", now: 0, id: "d", v: 1},
				{op: "dispatch", now: 0, n: 3, want: []Item{it("a", 3, 1), it("b", 5, 2), it("c", 6, 3)}},
				{op: "report", now: 10, id: "a", tok: 1, ver: 0, ok: false}, // attempts=1, readyAt=20
				// d (readyAt=0) is dispatched before a (readyAt=20).
				{op: "dispatch", now: 20, n: 2, want: []Item{it("d", 3, 4), it("a", 3, 5)}},
				// attempts reaches R: a Failed, failed=1 >= F=1, abort.
				{op: "report", now: 30, id: "a", tok: 5, ver: 0, ok: false},
				// success to a non-target hop after abort: Cancelled, version kept.
				{op: "report", now: 40, id: "b", tok: 2, ver: 5, ok: true},
				// success to T after abort: Done.
				{op: "report", now: 40, id: "c", tok: 3, ver: 6, ok: true},
				// failure below R after abort: Cancelled.
				{op: "report", now: 40, id: "d", tok: 4, ver: 0, ok: false},
				{op: "dispatch", now: 40, n: 1, wantErr: ErrAborted},
				// added after the abort: Cancelled.
				{op: "add", now: 40, id: "z", v: 1},
			},
			final: func(t *testing.T, c *Campaign) {
				checkDev(t, c, "a", Failed, 1, 2, 20)
				checkDev(t, c, "b", Cancelled, 5, 0, 0)
				checkDev(t, c, "c", Done, 6, 0, 0)
				checkDev(t, c, "d", Cancelled, 1, 1, 0)
				checkDev(t, c, "z", Cancelled, 1, 0, 40)
				if !c.aborted || c.failed != 1 {
					t.Errorf("aborted=%v failed=%d", c.aborted, c.failed)
				}
			},
		},
		{
			// Stale tokens and version mismatch.
			name: "stale token",
			T:    6, M: []int{3, 5}, C: 1, R: 2, B: 10, D: 100, F: 5,
			steps: []step{
				{op: "add", now: 0, id: "a", v: 1},
				{op: "dispatch", now: 0, n: 1, want: []Item{it("a", 3, 1)}},
				{op: "report", now: 5, id: "a", tok: 1, ver: 3, ok: true},
				{op: "dispatch", now: 10, n: 1, want: []Item{it("a", 5, 2)}},
				{op: "report", now: 15, id: "a", tok: 1, ver: 5, ok: true, wantErr: ErrStale},
				{op: "report", now: 15, id: "a", tok: 3, ver: 5, ok: true, wantErr: ErrStale},
				{op: "report", now: 15, id: "a", tok: 0, ver: 5, ok: true, wantErr: ErrStale},
				{op: "report", now: 15, id: "a", tok: 2, ver: 4, ok: true, wantErr: ErrVersion},
				// ok=false ignores ver entirely.
				{op: "report", now: 15, id: "a", tok: 2, ver: 999, ok: false},
			},
			final: func(t *testing.T, c *Campaign) {
				checkDev(t, c, "a", Pending, 3, 1, 25)
			},
		},
		{
			// Rejection order: only the first matching error is reported.
			name: "reject order",
			T:    6, M: []int{3, 5}, C: 2, R: 2, B: 10, D: 100, F: 2,
			steps: []step{
				{op: "add", now: 0, id: "a", v: 1},
				{op: "dispatch", now: 0, n: 1, want: []Item{it("a", 3, 1)}},
				{op: "report", now: 10, id: "a", tok: 1, ver: 3, ok: true},
				// ErrInvalid beats ErrClockBack.
				{op: "add", now: 5, id: "", v: 1, wantErr: ErrInvalid},
				{op: "add", now: 5, id: "x", v: 0, wantErr: ErrInvalid},
				// ErrClockBack beats ErrUnknown.
				{op: "add", now: 5, id: "x", v: 1, wantErr: ErrClockBack},
				{op: "report", now: 5, id: "ghost", tok: 1, ver: 0, ok: true, wantErr: ErrClockBack},
				// ErrUnknown beats ErrNotInFlight/ErrStale.
				{op: "report", now: 20, id: "ghost", tok: 1, ver: 0, ok: true, wantErr: ErrUnknown},
				{op: "add", now: 20, id: "b", v: 1},
				// ErrNotInFlight beats ErrStale.
				{op: "report", now: 20, id: "b", tok: 99, ver: 0, ok: true, wantErr: ErrNotInFlight},
				// duplicate id.
				{op: "add", now: 20, id: "b", v: 1, wantErr: ErrExists},
				{op: "dispatch", now: 30, n: 2, want: []Item{it("a", 5, 2), it("b", 3, 3)}},
				// ErrStale beats ErrVersion.
				{op: "report", now: 30, id: "a", tok: 99, ver: 99, ok: true, wantErr: ErrStale},
				{op: "report", now: 30, id: "a", tok: 2, ver: 99, ok: true, wantErr: ErrVersion},
				// drive the campaign into the abort.
				{op: "report", now: 30, id: "b", tok: 3, ver: 0, ok: false},
				{op: "dispatch", now: 40, n: 1, want: []Item{it("b", 3, 4)}},
				{op: "report", now: 50, id: "b", tok: 4, ver: 0, ok: false}, // b Failed, failed=1
				{op: "report", now: 50, id: "a", tok: 2, ver: 0, ok: false}, // a attempts=1, readyAt=60
				{op: "dispatch", now: 60, n: 1, want: []Item{it("a", 5, 5)}},
				{op: "report", now: 60, id: "a", tok: 5, ver: 0, ok: false}, // a Failed, failed=2 >= F: abort
				// ErrInvalid beats ErrAborted; ErrAborted last.
				{op: "dispatch", now: 60, n: 0, wantErr: ErrInvalid},
				{op: "dispatch", now: 60, n: 1, wantErr: ErrAborted},
				// ErrNotInFlight beats ErrAborted.
				{op: "report", now: 60, id: "b", tok: 4, ver: 0, ok: true, wantErr: ErrNotInFlight},
				{op: "add", now: 60, id: "z", v: 1},
			},
			final: func(t *testing.T, c *Campaign) {
				checkDev(t, c, "a", Failed, 3, 2, 60)
				checkDev(t, c, "b", Failed, 1, 2, 40)
				checkDev(t, c, "z", Cancelled, 1, 0, 60)
				if !c.aborted || c.failed != 2 {
					t.Errorf("aborted=%v failed=%d", c.aborted, c.failed)
				}
			},
		},
	}
	for _, sc := range scenarios {
		runScenario(t, sc)
	}
}

func TestNewValidation(t *testing.T) {
	type params struct {
		T    int
		M    []int
		C, R int
		B, D int64
		F    int
	}
	valid := params{T: 6, M: []int{3, 5}, C: 2, R: 2, B: 10, D: 100, F: 2}
	cases := []struct {
		name   string
		mutate func(p *params)
	}{
		{"T too small", func(p *params) { p.T = 1 }},
		{"T too big", func(p *params) { p.T = 1_000_001 }},
		{"M duplicate", func(p *params) { p.M = []int{3, 3} }},
		{"M not below T", func(p *params) { p.M = []int{6} }},
		{"M too many", func(p *params) { p.M = make([]int, 65) }},
		{"C zero", func(p *params) { p.C = 0 }},
		{"C too big", func(p *params) { p.C = 10_001 }},
		{"R zero", func(p *params) { p.R = 0 }},
		{"R too big", func(p *params) { p.R = 11 }},
		{"B zero", func(p *params) { p.B = 0 }},
		{"B too big", func(p *params) { p.B = 1_000_000_001 }},
		{"D zero", func(p *params) { p.D = 0 }},
		{"D too big", func(p *params) { p.D = 1_000_000_001 }},
		{"F zero", func(p *params) { p.F = 0 }},
		{"F too big", func(p *params) { p.F = 100_001 }},
	}
	for _, tc := range cases {
		p := valid
		tc.mutate(&p)
		if _, err := New(p.T, p.M, p.C, p.R, p.B, p.D, p.F); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	if _, err := New(valid.T, valid.M, valid.C, valid.R, valid.B, valid.D, valid.F); err != nil {
		t.Errorf("valid params: %v", err)
	}
}

// TestPoppedBound proves that the number of pops from the ready and
// deadline structures is bounded by the actual number of dispatched /
// timed-out devices (plus one), independent of the total device count:
// the same script runs with 100 and with 10000 devices and must
// produce identical popped counts.
func TestPoppedBound(t *testing.T) {
	for _, total := range []int{100, 10_000} {
		t.Run(fmt.Sprintf("devices=%d", total), func(t *testing.T) {
			c, err := New(1_000_000, nil, 10, 2, 10, 100, 100_000)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < total; i++ {
				if err := c.AddDevice(0, []byte(fmt.Sprintf("d%05d", i)), 1); err != nil {
					t.Fatal(err)
				}
			}
			check := func(label string, want int) {
				t.Helper()
				if c.popped != want {
					t.Fatalf("after %s: popped = %d, want %d", label, c.popped, want)
				}
			}
			dispatch := func(now int64, n int) {
				t.Helper()
				if _, err := c.Dispatch(now, n); err != nil {
					t.Fatal(err)
				}
			}
			dispatch(0, 3) // pops = 3 dispatched
			check("dispatch 3", 3)
			dispatch(0, 5) // pops = 5 dispatched
			check("dispatch 5 more", 8)
			// 8 in-flight devices time out at dl=100: pops = 8 timeouts.
			if err := c.AddDevice(100, []byte("zzzzz"), 1); err != nil {
				t.Fatal(err)
			}
			check("settle 8 timeouts", 16)
			dispatch(110, 3) // pops = 3 dispatched
			check("dispatch 3 again", 19)
		})
	}
}

// TestConcurrent hammers the campaign from many goroutines; with
// -race it proves the mutex serializes everything, and the final
// invariants prove the interleaving was equivalent to a serial order.
func TestConcurrent(t *testing.T) {
	c, err := New(1_000_000, nil, 4, 2, 5, 50, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = c.AddDevice(0, []byte(fmt.Sprintf("g%02d-%02d", g, i)), 1)
			}
		}(g)
	}
	wg.Wait()

	var mu sync.Mutex
	var toks []int
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				items, err := c.Dispatch(1, 3)
				if err != nil || len(items) == 0 {
					return
				}
				mu.Lock()
				for _, it := range items {
					toks = append(toks, it.Tok)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	sort.Ints(toks)
	for i, tk := range toks {
		if tk != i+1 {
			t.Fatalf("tokens not contiguous from 1: %v", toks)
		}
	}
	if got := c.slots.InFlight(); got != len(toks) || got > 4 {
		t.Fatalf("in-flight = %d, tokens = %d", got, len(toks))
	}
	checkInvariants(t, c)
}

// checkInvariants verifies the structural invariants of the campaign:
// the six states sum to the device total, in-flight count never
// exceeds C, the slot structure mirrors the InFlight devices, and the
// ready heap mirrors the Pending devices.
func checkInvariants(t *testing.T, c *Campaign) {
	t.Helper()
	counts := map[State]int{}
	inflight := 0
	pending := 0
	for _, d := range c.devices {
		counts[d.state]++
		switch d.state {
		case InFlight:
			inflight++
			dl, ok := c.slots.Deadline(d.id)
			if !ok || dl != d.dl {
				t.Errorf("slot deadline of %q = %d,%v, want %d", d.id, dl, ok, d.dl)
			}
		case Pending:
			pending++
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total != len(c.devices) {
		t.Errorf("state counts sum %d != device total %d", total, len(c.devices))
	}
	if inflight != c.slots.InFlight() {
		t.Errorf("in-flight devices %d != slot count %d", inflight, c.slots.InFlight())
	}
	if c.slots.Free() < 0 {
		t.Errorf("in-flight exceeds capacity: free = %d", c.slots.Free())
	}
	if len(c.ready) != pending {
		t.Errorf("ready heap %d != pending devices %d", len(c.ready), pending)
	}
	inHeap := map[string]bool{}
	for _, d := range c.ready {
		if d.state != Pending {
			t.Errorf("non-pending device %q in ready heap", d.id)
		}
		inHeap[d.id] = true
	}
	for _, d := range c.devices {
		if d.state == Pending && !inHeap[d.id] {
			t.Errorf("pending device %q missing from ready heap", d.id)
		}
	}
}
