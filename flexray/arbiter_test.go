package flexray

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestSkeleton(t *testing.T) {
	arbiter, err := New(1, 0, 0)
	if err != nil || arbiter == nil {
		t.Fatalf("New() = %v, %v", arbiter, err)
	}
}

func TestProvidedExample(t *testing.T) {
	arbiter := mustNew(t, 3, 6, 4)
	mustAssign(t, arbiter, 1, 0, 1)
	mustAssign(t, arbiter, 2, 1, 2)
	mustAssign(t, arbiter, 3, 0, 1)
	for id := 4; id <= 9; id++ {
		mustAssign(t, arbiter, id, 0, 1)
	}
	mustPost(t, arbiter, 1, 1, "a")
	mustPost(t, arbiter, 2, 1, "b")
	mustPost(t, arbiter, 4, 2, "m")
	mustPost(t, arbiter, 5, 2, "n")
	mustPost(t, arbiter, 7, 1, "p")
	mustPost(t, arbiter, 9, 1, "q")

	first := arbiter.Cycle()
	wantSent := []SentItem{
		{ID: 1, Tag: "a", Slot: 1},
		{ID: 4, Tag: "m", Start: 1},
		{ID: 5, Tag: "n", Start: 3},
	}
	assertResult(t, first, CycleResult{Cycle: 0, Sent: wantSent, UnusedSlots: 1, EmptyFrames: 1})
	assertPending(t, arbiter, 7, "p")
	assertPending(t, arbiter, 9, "q")

	second := arbiter.Cycle()
	wantSecond := []SentItem{
		{ID: 2, Tag: "b", Slot: 2},
		{ID: 7, Tag: "p", Start: 4},
	}
	assertResult(t, second, CycleResult{Cycle: 1, Sent: wantSecond, UnusedSlots: 2, EmptyFrames: 2})
	assertPending(t, arbiter, 9, "q")
}

func TestActivationAndCycleWrap(t *testing.T) {
	arbiter := mustNew(t, 1, 0, 0)
	mustAssign(t, arbiter, 1, 3, 4)
	mustPost(t, arbiter, 1, 0, "held")

	for cycle := 0; cycle < 3; cycle++ {
		result := arbiter.Cycle()
		assertResult(t, result, CycleResult{Cycle: cycle})
		assertPending(t, arbiter, 1, "held")
	}
	result := arbiter.Cycle()
	assertResult(t, result, CycleResult{Cycle: 3, Sent: []SentItem{{ID: 1, Tag: "held", Slot: 1}}})

	for cycle := 4; cycle < 63; cycle++ {
		result = arbiter.Cycle()
	}
	assertResult(t, result, CycleResult{Cycle: 62})
	mustPost(t, arbiter, 1, 0, "wrap")
	result = arbiter.Cycle()
	assertResult(t, result, CycleResult{Cycle: 63, Sent: []SentItem{{ID: 1, Tag: "wrap", Slot: 1}}})
	result = arbiter.Cycle()
	assertResult(t, result, CycleResult{Cycle: 0})
}

func TestStaticOverwriteAndInactiveRetention(t *testing.T) {
	arbiter := mustNew(t, 1, 0, 0)
	mustAssign(t, arbiter, 1, 1, 2)
	mustPost(t, arbiter, 1, -10, "old")
	mustPost(t, arbiter, 1, 999, "new")

	first := arbiter.Cycle()
	assertResult(t, first, CycleResult{Cycle: 0})
	assertPending(t, arbiter, 1, "new")
	if stats := arbiter.Stats(); stats != (StatsResult{Sent: 0, Empty: 0, Overwrites: 1}) {
		t.Fatalf("stats after inactive overwrite = %+v", stats)
	}

	second := arbiter.Cycle()
	assertResult(t, second, CycleResult{Cycle: 1, Sent: []SentItem{{ID: 1, Tag: "new", Slot: 1}}})
	assertPending(t, arbiter, 1)
}

func TestDynamicUnusedSlotRecoveryBoundaries(t *testing.T) {
	arbiter := mustNew(t, 3, 6, 6)
	mustAssign(t, arbiter, 1, 0, 1)
	mustAssign(t, arbiter, 2, 0, 1)
	mustAssign(t, arbiter, 3, 0, 1)
	mustPost(t, arbiter, 1, 1, "used")
	mustAssign(t, arbiter, 6, 0, 1)
	mustPost(t, arbiter, 6, 5, "long")

	result := arbiter.Cycle()
	assertResult(t, result, CycleResult{
		Cycle: 0,
		Sent: []SentItem{
			{ID: 1, Tag: "used", Slot: 1},
			{ID: 6, Tag: "long", Start: 3},
		},
		UnusedSlots: 2,
		EmptyFrames: 2,
	})

	for id := 1; id <= 3; id++ {
		mustPost(t, arbiter, id, 1, "used")
	}
	mustPost(t, arbiter, 6, 4, "exact")
	result = arbiter.Cycle()
	assertResult(t, result, CycleResult{
		Cycle: 1,
		Sent: []SentItem{
			{ID: 1, Tag: "used", Slot: 1},
			{ID: 2, Tag: "used", Slot: 2},
			{ID: 3, Tag: "used", Slot: 3},
			{ID: 6, Tag: "exact", Start: 3},
		},
	})

	for id := 1; id <= 3; id++ {
		mustPost(t, arbiter, id, 1, "used")
	}
	mustPost(t, arbiter, 6, 5, "too-large")
	result = arbiter.Cycle()
	assertResult(t, result, CycleResult{
		Cycle: 2,
		Sent: []SentItem{
			{ID: 1, Tag: "used", Slot: 1},
			{ID: 2, Tag: "used", Slot: 2},
			{ID: 3, Tag: "used", Slot: 3},
		},
	})
	assertPending(t, arbiter, 6, "too-large")
}

func TestDynamicLateThresholdAndFrameAdvance(t *testing.T) {
	arbiter := mustNew(t, 1, 8, 4)
	mustAssign(t, arbiter, 5, 0, 1)
	mustAssign(t, arbiter, 6, 0, 1)
	mustAssign(t, arbiter, 9, 0, 1)
	mustPost(t, arbiter, 5, 1, "equal-lt")
	mustPost(t, arbiter, 6, 1, "after-lt")
	mustPost(t, arbiter, 9, 1, "high-id")

	result := arbiter.Cycle()
	assertResult(t, result, CycleResult{
		Cycle: 0,
		Sent:  []SentItem{{ID: 5, Tag: "equal-lt", Start: 4}},
	})
	assertPending(t, arbiter, 6, "after-lt")
	assertPending(t, arbiter, 9, "high-id")

	arbiter2 := mustNew(t, 1, 8, 8)
	mustAssign(t, arbiter2, 2, 0, 1)
	mustAssign(t, arbiter2, 4, 0, 1)
	mustPost(t, arbiter2, 2, 3, "long")
	mustPost(t, arbiter2, 4, 1, "after-long")
	result = arbiter2.Cycle()
	assertResult(t, result, CycleResult{
		Cycle: 0,
		Sent: []SentItem{
			{ID: 2, Tag: "long", Start: 1},
			{ID: 4, Tag: "after-long", Start: 5},
		},
	})
}

func TestDegenerateDynamicSegment(t *testing.T) {
	noMinislots := mustNew(t, 1, 0, 0)
	mustAssign(t, noMinislots, 1, 0, 1)
	mustPost(t, noMinislots, 1, 7, "static")
	result := noMinislots.Cycle()
	assertResult(t, result, CycleResult{Cycle: 0, Sent: []SentItem{{ID: 1, Tag: "static", Slot: 1}}})

	blocked := mustNew(t, 1, 2, 0)
	mustAssign(t, blocked, 1, 0, 1)
	mustPost(t, blocked, 1, 1, "static-used")
	mustAssign(t, blocked, 2, 0, 1)
	mustPost(t, blocked, 2, 1, "never")
	result = blocked.Cycle()
	assertResult(t, result, CycleResult{Cycle: 0, Sent: []SentItem{{ID: 1, Tag: "static-used", Slot: 1}}})
	assertPending(t, blocked, 2, "never")
}

func TestDynamicSentIntervalsAreDisjoint(t *testing.T) {
	arbiter := mustNew(t, 1, 12, 12)
	mustAssign(t, arbiter, 1, 0, 1)
	mustPost(t, arbiter, 1, 1, "static")
	mustAssign(t, arbiter, 2, 0, 1)
	mustAssign(t, arbiter, 3, 0, 1)
	mustAssign(t, arbiter, 4, 0, 1)
	mustPost(t, arbiter, 2, 2, "a")
	mustPost(t, arbiter, 3, 3, "b")
	mustPost(t, arbiter, 4, 1, "c")

	result := arbiter.Cycle()
	if len(result.Sent) != 4 {
		t.Fatalf("sent = %+v", result.Sent)
	}
	intervals := []int{1, 3, 6}
	for index := 1; index < len(result.Sent); index++ {
		sent := result.Sent[index]
		if sent.Start != intervals[index-1] {
			t.Fatalf("sent[%d] start = %d, want %d: %+v", index, sent.Start, intervals[index-1], result.Sent)
		}
	}
}

func TestRejectionPrecedenceAndNoStateChange(t *testing.T) {
	arbiter := mustNew(t, 1, 1, 1)
	mustAssign(t, arbiter, 1, 0, 1)

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"constructor ns", func() error { _, err := New(0, 0, 0); return err }, ErrInvalidArgument},
		{"constructor lt", func() error { _, err := New(1, 1, 2); return err }, ErrInvalidArgument},
		{"assign base", func() error { return arbiter.Assign(2, 2, 2) }, ErrInvalidArgument},
		{"assign rep", func() error { return arbiter.Assign(2, 0, 3) }, ErrInvalidArgument},
		{"assign id", func() error { return arbiter.Assign(9, 0, 1) }, ErrInvalidID},
		{"duplicate id", func() error { return arbiter.Assign(1, 0, 1) }, ErrDuplicateID},
		{"post bad length", func() error { return arbiter.Post(2, 0, "x") }, ErrInvalidArgument},
		{"post static bad id ignores length", func() error { return arbiter.Post(0, 0, "x") }, ErrInvalidID},
		{"post id", func() error { return arbiter.Post(9, 1, "x") }, ErrInvalidID},
		{"post unassigned", func() error { return arbiter.Post(2, 1, "x") }, ErrNotAssigned},
	}
	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}

	mustAssign(t, arbiter, 2, 0, 1)
	for index := 0; index < 8; index++ {
		mustPost(t, arbiter, 2, 1, index)
	}
	if err := arbiter.Post(2, 1, "full"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v, want %v", err, ErrQueueFull)
	}
	pending := arbiter.Pending(2)
	if len(pending) != 8 || pending[7] != 7 {
		t.Fatalf("full queue pending = %v", pending)
	}
}

func TestConcurrentOperations(t *testing.T) {
	arbiter := mustNew(t, 4, 8, 8)
	for id := 1; id <= 12; id++ {
		mustAssign(t, arbiter, id, 0, 1)
	}

	var workers sync.WaitGroup
	run := func(body func()) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			body()
		}()
	}
	run(func() {
		for index := 0; index < 200; index++ {
			arbiter.Post(1+index%12, 1+(index%8), index)
		}
	})
	run(func() {
		for index := 0; index < 200; index++ {
			arbiter.Cycle()
		}
	})
	run(func() {
		for index := 0; index < 200; index++ {
			_ = arbiter.Pending(1 + index%12)
			_ = arbiter.Stats()
		}
	})
	workers.Wait()
}

type simulatedMessage struct {
	length int
	tag    int
}

type simulatedFrame struct {
	assigned bool
	base     int
	rep      int
	static   bool
	buffer   *simulatedMessage
	queue    []simulatedMessage
}

type simulatedArbiter struct {
	ns, nm, lt, cycleCount int
	frames                 []simulatedFrame
	sentTotal              int
	emptyTotal             int
	overwriteTotal         int
}

func newSimulatedArbiter(ns, nm, lt int) *simulatedArbiter {
	frames := make([]simulatedFrame, ns+nm+1)
	for id := 1; id <= ns+nm; id++ {
		frames[id].static = id <= ns
	}
	return &simulatedArbiter{ns: ns, nm: nm, lt: lt, frames: frames}
}

func (s *simulatedArbiter) assign(id, base, rep int) error {
	validRep := rep == 1 || rep == 2 || rep == 4 || rep == 8 || rep == 16 || rep == 32 || rep == 64
	if !validRep || base < 0 || base >= rep {
		return ErrInvalidArgument
	}
	if id < 1 || id > s.ns+s.nm {
		return ErrInvalidID
	}
	if s.frames[id].assigned {
		return ErrDuplicateID
	}
	s.frames[id].assigned = true
	s.frames[id].base = base
	s.frames[id].rep = rep
	return nil
}

func (s *simulatedArbiter) post(id, length, tag int) error {
	if id > s.ns && (length < 1 || length > s.nm) {
		return ErrInvalidArgument
	}
	if id < 1 || id > s.ns+s.nm {
		return ErrInvalidID
	}
	target := &s.frames[id]
	if !target.assigned {
		return ErrNotAssigned
	}
	if target.static {
		message := simulatedMessage{length: length, tag: tag}
		if target.buffer != nil {
			s.overwriteTotal++
		}
		target.buffer = &message
		return nil
	}
	if length < 1 || length > s.nm {
		return ErrInvalidArgument
	}
	if len(target.queue) == 8 {
		return ErrQueueFull
	}
	target.queue = append(target.queue, simulatedMessage{length: length, tag: tag})
	return nil
}

func (s *simulatedArbiter) cycle() CycleResult {
	result := CycleResult{Cycle: s.cycleCount}
	for id := 1; id <= s.ns; id++ {
		target := &s.frames[id]
		if !target.assigned || s.cycleCount%target.rep != target.base {
			continue
		}
		if target.buffer == nil {
			result.UnusedSlots++
			result.EmptyFrames++
			s.emptyTotal++
			continue
		}
		result.Sent = append(result.Sent, SentItem{ID: id, Tag: target.buffer.tag, Slot: id})
		target.buffer = nil
		s.sentTotal++
	}

	n := s.nm + result.UnusedSlots
	k := s.ns + 1
	i := 1
	for i <= n {
		if k <= s.ns+s.nm {
			target := &s.frames[k]
			if target.assigned && s.cycleCount%target.rep == target.base && len(target.queue) > 0 {
				message := target.queue[0]
				if i <= s.lt && i+message.length-1 <= n {
					result.Sent = append(result.Sent, SentItem{ID: k, Tag: message.tag, Start: i})
					target.queue = target.queue[1:]
					s.sentTotal++
					i += message.length
					k++
					continue
				}
			}
		}
		i++
		k++
	}

	s.cycleCount = (s.cycleCount + 1) % 64
	return result
}

func (s *simulatedArbiter) pending(id int) []SlotTag {
	if id < 1 || id > s.ns+s.nm {
		return nil
	}
	target := &s.frames[id]
	if target.static {
		if target.buffer == nil {
			return []SlotTag{}
		}
		return []SlotTag{target.buffer.tag}
	}
	pending := make([]SlotTag, len(target.queue))
	for index, message := range target.queue {
		pending[index] = message.tag
	}
	return pending
}

func TestRandomNaiveModelComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iteration := 0; iteration < 2000; iteration++ {
		ns := 1 + rng.Intn(4)
		nm := rng.Intn(9)
		lt := rng.Intn(nm + 1)
		arbiter, err := New(ns, nm, lt)
		if err != nil {
			t.Fatalf("iteration %d: New: %v", iteration, err)
		}
		model := newSimulatedArbiter(ns, nm, lt)
		var log []string
		log = append(log, fmt.Sprintf("ITER %d New(ns=%d,nm=%d,lt=%d) => success", iteration, ns, nm, lt))

		for step := 0; step < 32; step++ {
			id := 1 + rng.Intn(ns+nm+1)
			length := 1 + rng.Intn(nm+1)
			tag := iteration*1000 + step
			switch rng.Intn(4) {
			case 0:
				rep := []int{1, 2, 4, 8, 16, 32, 64}[rng.Intn(7)]
				base := rng.Intn(rep + 1)
				got := arbiter.Assign(id, base, rep)
				want := model.assign(id, base, rep)
				log = append(log, fmt.Sprintf("step %d Assign(id=%d,base=%d,rep=%d) => got=%v want=%v", step, id, base, rep, got, want))
				if !sameError(got, want) {
					t.Fatalf("iteration %d step %d Assign mismatch:\n%s", iteration, step, joinLines(log))
				}
			case 1:
				got := arbiter.Post(id, length, tag)
				want := model.post(id, length, tag)
				log = append(log, fmt.Sprintf("step %d Post(id=%d,L=%d,tag=%d) => got=%v want=%v", step, id, length, tag, got, want))
				if !sameError(got, want) {
					t.Fatalf("iteration %d step %d Post mismatch:\n%s", iteration, step, joinLines(log))
				}
			case 2:
				got := arbiter.Cycle()
				want := model.cycle()
				log = append(log, fmt.Sprintf("step %d Cycle() => got=%+v want=%+v; basis: static first, N=%d=nm+u, then i,k advance", step, got, want, nm+got.UnusedSlots))
				if !cycleResultsEqual(got, want) {
					t.Fatalf("iteration %d step %d Cycle mismatch:\n%s", iteration, step, joinLines(log))
				}
			case 3:
				got := arbiter.Pending(id)
				want := model.pending(id)
				log = append(log, fmt.Sprintf("step %d Pending(id=%d) => got=%v want=%v", step, id, got, want))
				if !tagsEqual(got, want) {
					t.Fatalf("iteration %d step %d Pending mismatch:\n%s", iteration, step, joinLines(log))
				}
			}
		}

		gotStats := arbiter.Stats()
		wantStats := StatsResult{Sent: model.sentTotal, Empty: model.emptyTotal, Overwrites: model.overwriteTotal}
		log = append(log, fmt.Sprintf("Stats() => got=%+v want=%+v; basis: cumulative counters", gotStats, wantStats))
		if gotStats != wantStats {
			t.Fatalf("iteration %d Stats mismatch:\n%s", iteration, joinLines(log))
		}
		if iteration < 20 {
			t.Log(joinLines(log))
		}
	}
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return errors.Is(got, want)
}

func cycleResultsEqual(got, want CycleResult) bool {
	if got.Cycle != want.Cycle || got.UnusedSlots != want.UnusedSlots || got.EmptyFrames != want.EmptyFrames || len(got.Sent) != len(want.Sent) {
		return false
	}
	for index := range got.Sent {
		if got.Sent[index] != want.Sent[index] {
			return false
		}
	}
	return true
}

func tagsEqual(got, want []SlotTag) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func joinLines(lines []string) string {
	result := ""
	for _, line := range lines {
		result += "\n" + line
	}
	return result
}

func mustNew(t *testing.T, ns, nm, lt int) *Arbiter {
	t.Helper()
	arbiter, err := New(ns, nm, lt)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", ns, nm, lt, err)
	}
	return arbiter
}

func mustAssign(t *testing.T, arbiter *Arbiter, id, base, rep int) {
	t.Helper()
	if err := arbiter.Assign(id, base, rep); err != nil {
		t.Fatalf("Assign(%d,%d,%d): %v", id, base, rep, err)
	}
}

func mustPost(t *testing.T, arbiter *Arbiter, id, length int, tag SlotTag) {
	t.Helper()
	if err := arbiter.Post(id, length, tag); err != nil {
		t.Fatalf("Post(%d,%d,%v): %v", id, length, tag, err)
	}
}

func assertResult(t *testing.T, got, want CycleResult) {
	t.Helper()
	if got.Cycle != want.Cycle || got.UnusedSlots != want.UnusedSlots || got.EmptyFrames != want.EmptyFrames || len(got.Sent) != len(want.Sent) {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	for index := range want.Sent {
		if got.Sent[index] != want.Sent[index] {
			t.Fatalf("sent[%d] = %+v, want %+v; full result %+v", index, got.Sent[index], want.Sent[index], got)
		}
	}
}

func assertPending(t *testing.T, arbiter *Arbiter, id int, want ...SlotTag) {
	t.Helper()
	got := arbiter.Pending(id)
	if len(got) != len(want) {
		t.Fatalf("Pending(%d) = %v; want %v", id, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Pending(%d)[%d] = %v, want %v; got %v", id, index, got[index], want[index], got)
		}
	}
}
