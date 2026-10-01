package waterfall

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveLayer is one layer of the step-by-step reference model.
type naiveLayer struct {
	cap  int64
	g    int
	recv int64
}

// naiveModel is a deliberately simple, single-threaded re-implementation of
// exactly the rules in the spec. Every move records its decision basis in
// steps so tests can log why the result looks the way it does.
type naiveModel struct {
	layers []naiveLayer
	steps  []string
}

func newNaive(caps []int64, g int) *naiveModel {
	layers := make([]naiveLayer, len(caps)+1)
	for i, c := range caps {
		layers[i] = naiveLayer{cap: c}
	}
	layers[len(caps)] = naiveLayer{g: g}
	return &naiveModel{layers: layers}
}

func (m *naiveModel) lastIdx() int { return len(m.layers) - 1 }

func (m *naiveModel) allocate(x int64) ([][2]int64, int64, int64) {
	m.steps = m.steps[:0]
	m.steps = append(m.steps, fmt.Sprintf("allocate %d: fill from layer 0", x))
	remaining := x
	for i := range m.layers {
		if remaining == 0 {
			break
		}
		if i == m.lastIdx() {
			m.layers[i].recv += remaining
			m.steps = append(m.steps, fmt.Sprintf("layer %d (remainder) swallows %d", i, remaining))
			break
		}
		room := m.layers[i].cap - m.layers[i].recv
		fill := remaining
		if fill > room {
			fill = room
		}
		m.layers[i].recv += fill
		remaining -= fill
		m.steps = append(m.steps, fmt.Sprintf("layer %d room=%d fills %d, leftover %d", i, room, fill, remaining))
	}
	r := m.layers[m.lastIdx()].recv
	manager := r * int64(m.layers[m.lastIdx()].g) / 100
	m.steps = append(m.steps, fmt.Sprintf("remainder cumulative R=%d manager=floor(%d*%d/100)=%d investor=%d",
		r, r, m.layers[m.lastIdx()].g, manager, r-manager))
	return m.snapshot(), manager, r - manager
}

func (m *naiveModel) clawback(y int64) ([][2]int64, int64, int64) {
	m.steps = m.steps[:0]
	m.steps = append(m.steps, fmt.Sprintf("clawback %d: deduct from last layer backwards", y))
	remaining := y
	for i := m.lastIdx(); i >= 0; i-- {
		if remaining == 0 {
			break
		}
		cut := remaining
		if cut > m.layers[i].recv {
			cut = m.layers[i].recv
		}
		m.layers[i].recv -= cut
		remaining -= cut
		m.steps = append(m.steps, fmt.Sprintf("layer %d cuts %d, leftover %d", i, cut, remaining))
	}
	r := m.layers[m.lastIdx()].recv
	manager := r * int64(m.layers[m.lastIdx()].g) / 100
	m.steps = append(m.steps, fmt.Sprintf("remainder cumulative R=%d manager=%d investor=%d", r, manager, r-manager))
	return m.snapshot(), manager, r - manager
}

func (m *naiveModel) snapshot() [][2]int64 {
	out := make([][2]int64, len(m.layers))
	for i, l := range m.layers {
		out[i] = [2]int64{int64(i), l.recv}
	}
	return out
}

func recvRows(s *State) [][2]int64 {
	out := make([][2]int64, len(s.Layers))
	for i, l := range s.Layers {
		out[i] = [2]int64{int64(i), l.Recv}
	}
	return out
}

func logResult(t *testing.T, op string, amount int64, s *State, err error, basis []string) {
	t.Helper()
	if err != nil {
		t.Logf("INPUT  %s amount=%d", op, amount)
		t.Logf("OUTPUT rejected: %v", err)
		for _, b := range basis {
			t.Logf("BASIS  %s", b)
		}
		return
	}
	recvs := make([]int64, len(s.Layers))
	for i, l := range s.Layers {
		recvs[i] = l.Recv
	}
	t.Logf("INPUT  %s amount=%d", op, amount)
	t.Logf("OUTPUT recv=%v split={R:%d manager:%d investor:%d}",
		recvs, s.Split.Received, s.Split.Manager, s.Split.Investor)
	for _, b := range basis {
		t.Logf("BASIS  %s", b)
	}
}

func assertRows(t *testing.T, got, want [][2]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("layer count got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i][1] != want[i][1] {
			t.Fatalf("layer %d recv got %d want %d (rows got=%v want=%v)",
				i, got[i][1], want[i][1], got, want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name   string
		layers []Layer
	}{
		{"one layer", []Layer{{Cap: 10, G: 20}}},
		{"negative cap", []Layer{{Cap: -1}, {G: 20}}},
		{"g negative", []Layer{{Cap: 10}, {G: -1}}},
		{"g over 100", []Layer{{Cap: 10}, {G: 101}}},
		{"nonzero initial recv", []Layer{{Cap: 10, Recv: 1}, {G: 20}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.layers); err != ErrInvalidConfig {
				t.Fatalf("got %v want ErrInvalidConfig", err)
			}
		})
	}
	for _, g := range []int{0, 50, 100} {
		if _, err := New([]Layer{{Cap: 10}, {G: g}}); err != nil {
			t.Fatalf("g=%d should be valid, got %v", g, err)
		}
	}
}

func TestWaterfallVsNaive(t *testing.T) {
	w, err := New([]Layer{{Cap: 10}, {Cap: 20}, {G: 20}})
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive([]int64{10, 20}, 20)

	type op struct {
		kind string
		amt  int64
	}
	script := []op{
		{"alloc", 10}, // exactly fills layer 0
		{"alloc", 25}, // crosses: layer 1 filled exactly (20), 5 to remainder
		{"claw", 3},   // only last layer has money, deduction stays there
		{"claw", 25},  // reverse across remainder -> layer 1 -> layer 0
		{"alloc", 8},  // re-allocation refills freed layer 0 (3) then layer 1 (5)
		{"claw", 15},  // layer 1 loses 5, layer 0 loses 10
		{"alloc", 12}, // layer 0 fills 10, layer 1 gets 2
	}

	var trace []*State
	for _, step := range script {
		var s *State
		var err error
		var want [][2]int64
		var wantMgr, wantInv int64
		if step.kind == "alloc" {
			s, err = w.Allocate(step.amt)
			want, wantMgr, wantInv = m.allocate(step.amt)
		} else {
			s, err = w.Clawback(step.amt)
			want, wantMgr, wantInv = m.clawback(step.amt)
		}
		logResult(t, step.kind, step.amt, s, err, m.steps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRows(t, recvRows(s), want)
		if s.Split.Manager != wantMgr || s.Split.Investor != wantInv {
			t.Fatalf("split got mgr=%d inv=%d want mgr=%d inv=%d",
				s.Split.Manager, s.Split.Investor, wantMgr, wantInv)
		}
		trace = append(trace, s)
	}

	// Replay the identical script: every snapshot must be identical.
	w2, _ := New([]Layer{{Cap: 10}, {Cap: 20}, {G: 20}})
	for i, step := range script {
		var s *State
		var err error
		if step.kind == "alloc" {
			s, err = w2.Allocate(step.amt)
		} else {
			s, err = w2.Clawback(step.amt)
		}
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, recvRows(s), recvRows(trace[i]))
		if s.Split != trace[i].Split {
			t.Fatalf("step %d split mismatch on replay: %+v vs %+v",
				i, s.Split, trace[i].Split)
		}
	}
	t.Logf("replay of %d operations produced identical layer recv and splits", len(script))
}

func TestCumulativeVsPerTickSplit(t *testing.T) {
	w, err := New([]Layer{{Cap: 100}, {G: 20}})
	if err != nil {
		t.Fatal(err)
	}

	s, _ := w.Allocate(100) // fills layer 0 exactly; nothing reaches remainder
	logResult(t, "alloc", 100, s, nil, []string{
		"layer 0 cap 100 filled exactly; remainder R=0",
	})

	s, _ = w.Allocate(4)
	perTick := int64(4) * 20 / 100
	logResult(t, "alloc", 4, s, nil, []string{
		"R=4: cumulative manager floor(4*20/100)=0",
		fmt.Sprintf("per-tick accounting gives %d here too", perTick),
	})

	s, _ = w.Allocate(4)
	// Cumulative basis: floor(8*20/100) = 1. Per-ticket summation: 0 + 0 = 0.
	perTick = 4*20/100 + 4*20/100
	logResult(t, "alloc", 4, s, nil, []string{
		"R=8: cumulative manager floor(8*20/100)=1, investor=7",
		fmt.Sprintf("per-tick accounting would give floor(4*20/100)+floor(4*20/100)=%d (must NOT be used)", perTick),
	})
	if s.Split.Manager != 1 || s.Split.Investor != 7 {
		t.Fatalf("got manager=%d investor=%d, want 1/7", s.Split.Manager, s.Split.Investor)
	}
	if perTick != 0 {
		t.Fatalf("per-tick reference expected 0, got %d", perTick)
	}

	// Clawback lowers R from 8 to 4; the manager share must drop 1 -> 0.
	s, _ = w.Clawback(4)
	logResult(t, "claw", 4, s, nil, []string{
		"4 deducted from last layer; R falls 8 -> 4",
		"split recomputed cumulatively: floor(4*20/100)=0 (manager dropped from 1)",
	})
	if s.Split.Received != 4 || s.Split.Manager != 0 || s.Split.Investor != 4 {
		t.Fatalf("after clawback got %+v, want R=4 manager=0 investor=4", s.Split)
	}
}

func TestRejectedOperations(t *testing.T) {
	w, err := New([]Layer{{Cap: 10}, {G: 20}})
	if err != nil {
		t.Fatal(err)
	}

	// Non-positive is checked first, even when a clawback would also exceed
	// the zero total.
	for _, amt := range []int64{0, -1, -100} {
		before := w.Query()
		if _, err := w.Allocate(amt); err != ErrNonPositiveAmount {
			t.Fatalf("allocate %d: got %v want ErrNonPositiveAmount", amt, err)
		}
		if _, err := w.Clawback(amt); err != ErrNonPositiveAmount {
			t.Fatalf("clawback %d: got %v want ErrNonPositiveAmount", amt, err)
		}
		logResult(t, "non-positive", amt, nil, ErrNonPositiveAmount,
			[]string{"amount <= 0 is rejected before any balance/total check"})
		assertRows(t, recvRows(w.Query()), recvRows(before))
	}

	// A positive clawback over the current total is a distinct reason.
	before := w.Query()
	_, err = w.Clawback(1)
	logResult(t, "claw", 1, nil, err,
		[]string{"amount positive; total received 0 < 1 -> exceeds total"})
	if err != ErrClawbackExceedsReceived {
		t.Fatalf("got %v want ErrClawbackExceedsReceived", err)
	}
	assertRows(t, recvRows(w.Query()), recvRows(before))

	// Exactly 1e15 is allowed; the next unit is rejected.
	s, err := w.Allocate(MaxTotal)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "alloc", MaxTotal, s, nil,
		[]string{fmt.Sprintf("new total %d == limit, accepted", MaxTotal)})
	if s.Layers[0].Recv+s.Split.Received != MaxTotal {
		t.Fatal("total not at limit")
	}
	before = w.Query()
	_, err = w.Allocate(1)
	logResult(t, "alloc", 1, nil, err,
		[]string{fmt.Sprintf("new total %d > limit %d -> rejected", MaxTotal+1, MaxTotal)})
	if err != ErrTotalExceedsLimit {
		t.Fatalf("got %v want ErrTotalExceedsLimit", err)
	}
	assertRows(t, recvRows(w.Query()), recvRows(before))

	// After a full clawback, allocation restarts filling at layer 0.
	s, err = w.Clawback(MaxTotal)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "claw", MaxTotal, s, nil,
		[]string{"full reverse: remainder first, then capped layers backwards"})
	s, err = w.Allocate(3)
	if err != nil {
		t.Fatal(err)
	}
	logResult(t, "alloc", 3, s, nil,
		[]string{"after clawback filling restarts at layer 0 (cap 10), not at the remainder"})
	if s.Layers[0].Recv != 3 || s.Split.Received != 0 {
		t.Fatalf("got layer0=%d remainder=%d, want 3 and 0", s.Layers[0].Recv, s.Split.Received)
	}

	// A positive over-total clawback still changes nothing.
	before = w.Query()
	_, err = w.Clawback(4)
	logResult(t, "claw", 4, nil, err,
		[]string{"amount positive; total received 3 < 4 -> exceeds total; state untouched"})
	if err != ErrClawbackExceedsReceived {
		t.Fatalf("got %v want ErrClawbackExceedsReceived", err)
	}
	assertRows(t, recvRows(w.Query()), recvRows(before))
}

func TestInvariants(t *testing.T) {
	w, err := New([]Layer{{Cap: 10}, {Cap: 20}, {Cap: 30}, {G: 33}})
	if err != nil {
		t.Fatal(err)
	}

	script := []struct {
		kind string
		amt  int64
	}{
		{"alloc", 5}, {"alloc", 12}, {"alloc", 40}, {"claw", 7},
		{"alloc", 9}, {"claw", 50}, {"alloc", 100}, {"claw", 60},
	}
	var allocated, clawed int64
	for _, step := range script {
		var s *State
		var err error
		if step.kind == "alloc" {
			s, err = w.Allocate(step.amt)
			allocated += step.amt
		} else {
			s, err = w.Clawback(step.amt)
			clawed += step.amt
		}
		if err != nil {
			t.Fatalf("%s %d: %v", step.kind, step.amt, err)
		}
		var sum int64
		for i, l := range s.Layers {
			sum += l.Recv
			if i < len(s.Layers)-1 && l.Recv > l.Cap {
				t.Fatalf("layer %d recv %d exceeds cap %d", i, l.Recv, l.Cap)
			}
		}
		if sum != allocated-clawed {
			t.Fatalf("sum %d != allocated %d - clawed %d", sum, allocated, clawed)
		}
		g := int64(s.Layers[len(s.Layers)-1].G)
		manager := s.Split.Received * g / 100
		if s.Split.Manager != manager || s.Split.Manager+s.Split.Investor != s.Split.Received {
			t.Fatalf("bad split %+v", s.Split)
		}
	}
}

func TestConcurrent(t *testing.T) {
	w, err := New([]Layer{{Cap: 3000}, {Cap: 3000}, {G: 20}})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 12
	const rounds = 200
	var violations int64
	var wg sync.WaitGroup

	check := func(s *State) {
		var sum int64
		for i, l := range s.Layers {
			sum += l.Recv
			if i < len(s.Layers)-1 && l.Recv > l.Cap {
				atomic.AddInt64(&violations, 1)
			}
		}
		if sum > MaxTotal {
			atomic.AddInt64(&violations, 1)
		}
		if s.Split.Manager+s.Split.Investor != s.Split.Received ||
			s.Split.Manager != s.Split.Received*int64(s.Layers[len(s.Layers)-1].G)/100 {
			atomic.AddInt64(&violations, 1)
		}
	}

	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				switch (id + r) % 4 {
				case 0:
					if s, aerr := w.Allocate(int64(1 + (id*7+r)%5)); aerr == nil {
						check(s)
					}
				case 1:
					if s, cerr := w.Clawback(int64(1 + (id*3+r)%5)); cerr == nil {
						check(s)
					} else if cerr != ErrClawbackExceedsReceived {
						t.Errorf("unexpected clawback error: %v", cerr)
					}
				default:
					check(w.Query())
				}
			}
		}(k)
	}
	wg.Wait()

	if violations != 0 {
		t.Fatalf("observed %d invariant violations under concurrency", violations)
	}
	s := w.Query()
	var sum int64
	for _, l := range s.Layers {
		sum += l.Recv
	}
	t.Logf("concurrent run finished: layers recv with total %d, split %+v", sum, s.Split)
}
