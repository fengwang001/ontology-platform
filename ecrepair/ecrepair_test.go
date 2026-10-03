package ecrepair

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, k, m, tau, A, R, Cap, D, Q int) *Scheduler {
	t.Helper()
	s, err := New(k, m, tau, A, R, Cap, D, Q)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d,%d,%d,%d): %v", k, m, tau, A, R, Cap, D, Q, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Scheduler, now, id int) {
	t.Helper()
	if err := s.AddStripe(now, id); err != nil {
		t.Fatalf("AddStripe(%d,%d): %v", now, id, err)
	}
}

func mustLose(t *testing.T, s *Scheduler, now, id, shard int) {
	t.Helper()
	if err := s.Lose(now, id, shard); err != nil {
		t.Fatalf("Lose(%d,%d,%d): %v", now, id, shard, err)
	}
}

func mustTick(t *testing.T, s *Scheduler, now int) Report {
	t.Helper()
	rep, err := s.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	return rep
}

func wantStatus(t *testing.T, s *Scheduler, id int, want Status) {
	t.Helper()
	got, err := s.Status(id)
	if err != nil {
		t.Fatalf("Status(%d): %v", id, err)
	}
	if got != want {
		t.Fatalf("Status(%d) = %+v, want %+v", id, got, want)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func emptyReport() Report {
	return Report{Completed: []int{}, Started: []RepairStart{}}
}

func TestNewParamValidation(t *testing.T) {
	if _, err := New(4, 2, 1, 100, 4, 8, 3, 2); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if _, err := New(1, 1, 0, 1, 1, 1, 1, 1); err != nil {
		t.Fatalf("minimal valid params rejected: %v", err)
	}
	if _, err := New(16, 16, 16, 1, 1, 1e9, 1, 1); err != nil {
		t.Fatalf("boundary valid params rejected: %v", err)
	}
	bad := [][8]int{
		{0, 2, 1, 100, 4, 8, 3, 2},       // k < 1
		{4, 0, 1, 100, 4, 8, 3, 2},       // m < 1
		{32, 1, 1, 100, 4, 8, 3, 2},      // n > 32
		{4, 2, -1, 100, 4, 8, 3, 2},      // tau < 0
		{4, 2, 3, 100, 4, 8, 3, 2},       // tau > m
		{4, 2, 1, 0, 4, 8, 3, 2},         // A < 1
		{4, 2, 1, 100, 0, 8, 3, 2},       // R < 1
		{4, 2, 1, 100, 4, 3, 3, 2},       // Cap < R
		{4, 2, 1, 100, 4, 1e9 + 1, 3, 2}, // Cap > 1e9
		{4, 2, 1, 100, 4, 8, 0, 2},       // D < 1
		{4, 2, 1, 100, 4, 8, 3, 0},       // Q < 1
	}
	for i, p := range bad {
		if _, err := New(p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7]); !errors.Is(err, ErrParam) {
			t.Fatalf("case %d: err = %v, want ErrParam", i, err)
		}
	}
}

// The worked example from the spec: k=4 m=2 tau=1 A=100 R=4 Cap=8 D=3 Q=2.
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 4, 2, 1, 100, 4, 8, 3, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 1, 1, 0)
	mustLose(t, s, 2, 2, 1)
	mustLose(t, s, 2, 2, 4)

	wantStatus(t, s, 1, Status{Degraded, 5, 1, 0, 1, 1})
	wantStatus(t, s, 2, Status{Degraded, 4, 2, 0, 0, 2})

	rep := mustTick(t, s, 2)
	want := Report{
		Completed: []int{},
		Started: []RepairStart{
			{ID: 2, Shards: []int{1, 4}, Finish: 5},
			{ID: 1, Shards: []int{0}, Finish: 5},
		},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(2) = %+v, want %+v", rep, want)
	}
	if got := s.Tokens(); got != 0 {
		t.Fatalf("Tokens() = %d, want 0", got)
	}
	wantStatus(t, s, 2, Status{Repairing, 4, 0, 2, 0, 2})

	rep = mustTick(t, s, 5)
	want = Report{Completed: []int{1, 2}, Started: []RepairStart{}}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(5) = %+v, want %+v", rep, want)
	}
	if got := s.Tokens(); got != 8 {
		t.Fatalf("Tokens() = %d, want 8", got)
	}
	wantStatus(t, s, 1, Status{Healthy, 6, 0, 0, 2, -1})
	wantStatus(t, s, 2, Status{Healthy, 6, 0, 0, 2, -1})
}

// Tier 0 (aged out) beats a tier-1 stripe with a smaller margin.
func TestTierOverridesMargin(t *testing.T) {
	s := mustNew(t, 4, 3, 0, 10, 100, 1000, 5, 2)
	mustAdd(t, s, 0, 1) // X
	mustAdd(t, s, 0, 2) // Y
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 9, 2, 1)
	mustLose(t, s, 9, 2, 2)
	mustLose(t, s, 9, 2, 3)

	rep := mustTick(t, s, 10)
	want := Report{
		Completed: []int{},
		Started: []RepairStart{
			{ID: 1, Shards: []int{0}, Finish: 15},
			{ID: 2, Shards: []int{1, 2, 3}, Finish: 15},
		},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(10) = %+v, want %+v", rep, want)
	}
}

// A stripe with alive < k but an in-flight repair is not Dead; after the
// repair finishes it is Degraded (not Dead) and keeps its firstLost.
func TestDelayedDeadDuringRepair(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 1, 100, 1, 1)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 1, 1, 0)

	rep := mustTick(t, s, 2) // tokens = 2 == k: repair starts, tokens = 0
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 1, Shards: []int{0}, Finish: 3}}) {
		t.Fatalf("Tick(2).Started = %+v", rep.Started)
	}

	mustLose(t, s, 2, 1, 1)
	wantStatus(t, s, 1, Status{Repairing, 1, 1, 1, -1, 1})

	// Completion: shard 0 back, alive = 2 = k, still one Lost. Tokens are
	// only 1 < k, so no new repair starts and the stripe stays Degraded.
	rep = mustTick(t, s, 3)
	if !reflect.DeepEqual(rep.Completed, []int{1}) {
		t.Fatalf("Tick(3).Completed = %+v", rep.Completed)
	}
	wantStatus(t, s, 1, Status{Degraded, 2, 1, 0, 0, 1})
}

// margin == tau is eligible; margin == tau+1 is not (until it ages out).
func TestMarginBoundaryTau(t *testing.T) {
	s := mustNew(t, 4, 3, 1, 1000, 100, 1000, 5, 10)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 1, 1) // stripe 1: margin = 5-4 = 1 == tau
	mustLose(t, s, 0, 2, 0) // stripe 2: margin = 6-4 = 2 == tau+1

	rep := mustTick(t, s, 1)
	want := Report{
		Completed: []int{},
		Started:   []RepairStart{{ID: 1, Shards: []int{0, 1}, Finish: 6}},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(1) = %+v, want %+v", rep, want)
	}
	wantStatus(t, s, 2, Status{Degraded, 6, 1, 0, 2, 0})
}

// now-firstLost == A (exactly) forces repair even when margin > tau.
func TestAgeExactlyA(t *testing.T) {
	s := mustNew(t, 2, 2, 0, 10, 1, 100, 3, 5)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 0, 1, 0) // margin = 3-2 = 1 > tau = 0

	rep := mustTick(t, s, 9) // age 9 < A: not eligible
	if !reflect.DeepEqual(rep, emptyReport()) {
		t.Fatalf("Tick(9) = %+v, want empty", rep)
	}
	rep = mustTick(t, s, 10) // age 10 == A: eligible, tier 0
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 1, Shards: []int{0}, Finish: 13}}) {
		t.Fatalf("Tick(10).Started = %+v", rep.Started)
	}
}

// Equal margins: the stripe with the smaller firstLost goes first.
func TestSameMarginFirstLostOrder(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 100, 1000, 2, 1)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 2, 2, 0) // firstLost 2
	mustLose(t, s, 5, 1, 0) // firstLost 5

	rep := mustTick(t, s, 10) // Q=1: only the older stripe starts
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 2, Shards: []int{0}, Finish: 12}}) {
		t.Fatalf("Tick(10).Started = %+v", rep.Started)
	}
	rep = mustTick(t, s, 12) // stripe 2 finishes, stripe 1 starts
	want := Report{
		Completed: []int{2},
		Started:   []RepairStart{{ID: 1, Shards: []int{0}, Finish: 14}},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(12) = %+v, want %+v", rep, want)
	}
}

// Scheduling stops when in-flight repairs reach Q, but still starts at Q-1.
func TestInflightQLimit(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 100, 1000, 5, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustAdd(t, s, 0, 3)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 2, 0)
	mustLose(t, s, 0, 3, 0)

	rep := mustTick(t, s, 1)
	if !reflect.DeepEqual(rep.Started, []RepairStart{
		{ID: 1, Shards: []int{0}, Finish: 6},
		{ID: 2, Shards: []int{0}, Finish: 6},
	}) {
		t.Fatalf("Tick(1).Started = %+v", rep.Started)
	}

	// In-flight == Q: nothing starts even though tokens are plentiful.
	rep = mustTick(t, s, 2)
	if !reflect.DeepEqual(rep, emptyReport()) {
		t.Fatalf("Tick(2) = %+v, want empty", rep)
	}

	// Both repairs finish at 6; stripe 3 (in-flight == Q-1 after the first
	// start) must still be scheduled.
	rep = mustTick(t, s, 6)
	want := Report{
		Completed: []int{1, 2},
		Started:   []RepairStart{{ID: 3, Shards: []int{0}, Finish: 11}},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Fatalf("Tick(6) = %+v, want %+v", rep, want)
	}
}

// A repair starts when tokens == k exactly, leaving 0 tokens.
func TestTokensExactlyK(t *testing.T) {
	s := mustNew(t, 3, 1, 0, 1000, 3, 100, 2, 5)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 0, 1, 0)
	mustLose(t, s, 0, 2, 0)

	rep := mustTick(t, s, 1) // tokens = 3 == k
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 1, Shards: []int{0}, Finish: 3}}) {
		t.Fatalf("Tick(1).Started = %+v", rep.Started)
	}
	if got := s.Tokens(); got != 0 {
		t.Fatalf("Tokens() = %d, want 0", got)
	}
	wantStatus(t, s, 2, Status{Degraded, 3, 1, 0, 0, 0})
}

// Tokens are capped at Cap and accumulate over multi-unit deltas.
func TestTokenCapAndDelta(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 4, 8, 2, 5)
	rep := mustTick(t, s, 10) // 4*10 = 40, capped at 8
	if !reflect.DeepEqual(rep, emptyReport()) {
		t.Fatalf("Tick(10) = %+v, want empty", rep)
	}
	if got := s.Tokens(); got != 8 {
		t.Fatalf("Tokens() = %d, want 8", got)
	}
	mustTick(t, s, 12) // 8 + 4*2 = 16, still capped at 8
	if got := s.Tokens(); got != 8 {
		t.Fatalf("Tokens() = %d, want 8", got)
	}

	s2 := mustNew(t, 2, 1, 0, 1000, 2, 100, 2, 5)
	mustTick(t, s2, 5) // delta spans 5 units: 2*5 = 10
	if got := s2.Tokens(); got != 10 {
		t.Fatalf("Tokens() = %d, want 10", got)
	}
	mustTick(t, s2, 5) // delta 0: unchanged
	if got := s2.Tokens(); got != 10 {
		t.Fatalf("Tokens() = %d, want 10", got)
	}
}

// Repairs finishing at the same instant complete in ascending stripe id.
func TestSameFinishOrder(t *testing.T) {
	s := mustNew(t, 2, 2, 2, 1000, 100, 1000, 4, 5)
	mustAdd(t, s, 0, 5)
	mustAdd(t, s, 0, 3)
	mustLose(t, s, 0, 5, 0)
	mustLose(t, s, 0, 3, 0)

	rep := mustTick(t, s, 1)
	if !reflect.DeepEqual(rep.Started, []RepairStart{
		{ID: 3, Shards: []int{0}, Finish: 5},
		{ID: 5, Shards: []int{0}, Finish: 5},
	}) {
		t.Fatalf("Tick(1).Started = %+v", rep.Started)
	}
	rep = mustTick(t, s, 5)
	if !reflect.DeepEqual(rep.Completed, []int{3, 5}) {
		t.Fatalf("Tick(5).Completed = %+v, want [3 5]", rep.Completed)
	}
}

// alive < k after a repair completes turns the stripe Dead; Dead is sticky,
// rejects Lose and never participates in scheduling again.
func TestDeadAfterCompletion(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 10, 100, 5, 5)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)
	mustLose(t, s, 1, 1, 0)

	rep := mustTick(t, s, 1)
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 1, Shards: []int{0}, Finish: 6}}) {
		t.Fatalf("Tick(1).Started = %+v", rep.Started)
	}

	// Two more losses during the repair: alive = 0 < k, but the in-flight
	// repair delays the Dead verdict.
	mustLose(t, s, 2, 1, 1)
	mustLose(t, s, 2, 1, 2)
	wantStatus(t, s, 1, Status{Repairing, 0, 2, 1, -2, 1})

	// Completion brings shard 0 back: alive = 1 < k, no in-flight -> Dead.
	rep = mustTick(t, s, 6)
	if !reflect.DeepEqual(rep.Completed, []int{1}) {
		t.Fatalf("Tick(6).Completed = %+v", rep.Completed)
	}
	wantStatus(t, s, 1, Status{Dead, 1, 2, 0, -1, 1})

	// Dead stripes reject further losses and are never scheduled.
	wantErr(t, s.Lose(7, 1, 0), ErrDead)
	mustLose(t, s, 7, 2, 0) // give stripe 2 something to do
	rep = mustTick(t, s, 8)
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 2, Shards: []int{0}, Finish: 13}}) {
		t.Fatalf("Tick(8).Started = %+v", rep.Started)
	}
	wantStatus(t, s, 1, Status{Dead, 1, 2, 0, -1, 1})
}

// A stripe can also turn Dead directly after a Lose (no in-flight repair).
func TestDeadOnLose(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 10, 100, 5, 5)
	mustAdd(t, s, 0, 1)
	mustLose(t, s, 1, 1, 0)
	mustLose(t, s, 1, 1, 1) // alive = 1 < k = 2, no repair in flight
	wantStatus(t, s, 1, Status{Dead, 1, 2, 0, -1, 1})

	rep := mustTick(t, s, 2) // Dead stripes are not repair candidates.
	if !reflect.DeepEqual(rep, emptyReport()) {
		t.Fatalf("Tick(2) = %+v, want empty", rep)
	}
}

// Rejected operations must not change state, clock or the token baseline.
func TestRejectedOpsNoStateChange(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 1, 100, 5, 5)
	mustAdd(t, s, 5, 1)

	wantErr(t, s.AddStripe(-1, 2), ErrParam) // negative now
	wantErr(t, s.AddStripe(5, 0), ErrParam)  // non-positive id
	wantErr(t, s.AddStripe(4, 2), ErrClock)  // clock regression
	wantErr(t, s.AddStripe(5, 1), ErrExists) // duplicate id
	wantErr(t, s.Lose(5, 1, 3), ErrParam)    // shard out of range
	wantErr(t, s.Lose(5, 1, -1), ErrParam)   // negative shard
	wantErr(t, s.Lose(5, 9, 0), ErrUnknown)  // unknown stripe
	wantErr(t, s.Lose(4, 1, 0), ErrClock)    // clock regression
	_, err := s.Tick(-1)
	wantErr(t, err, ErrParam) // negative now
	_, err = s.Tick(4)
	wantErr(t, err, ErrClock) // clock regression
	_, err = s.Status(0)
	wantErr(t, err, ErrParam) // non-positive id
	if _, err := s.Status(9); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Status(9) err = %v, want ErrUnknown", err)
	}

	// Nothing moved: clock still at 5, token baseline still at 0.
	mustLose(t, s, 5, 1, 0)
	wantStatus(t, s, 1, Status{Degraded, 2, 1, 0, 0, 5})
	wantErr(t, s.Lose(5, 1, 0), ErrNotAlive) // already Lost
	rep := mustTick(t, s, 5)                 // tokens = 1*5 = 5
	if !reflect.DeepEqual(rep.Started, []RepairStart{{ID: 1, Shards: []int{0}, Finish: 10}}) {
		t.Fatalf("Tick(5).Started = %+v", rep.Started)
	}
	if got := s.Tokens(); got != 3 {
		t.Fatalf("Tokens() = %d, want 3", got)
	}
}

// A rejected Tick must not advance the token accumulation baseline.
func TestRejectedTickKeepsBaseline(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 1, 100, 5, 5)
	mustTick(t, s, 5) // tokens = 5, lastTick = 5
	_, err := s.Tick(3)
	wantErr(t, err, ErrClock)
	mustTick(t, s, 7) // delta from 5, not from 3
	if got := s.Tokens(); got != 7 {
		t.Fatalf("Tokens() = %d, want 7", got)
	}
}

// Concurrent calls must behave like some serial execution; run with -race.
func TestConcurrentAccess(t *testing.T) {
	s := mustNew(t, 2, 1, 0, 1000, 2, 100, 3, 2)
	mustAdd(t, s, 0, 1)
	mustAdd(t, s, 0, 2)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = s.Status(1)
				_ = s.Tokens()
				_, _ = s.Tick(i) // may fail with ErrClock; that is fine
			}
		}(g)
	}
	wg.Wait()
}
