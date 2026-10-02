package isolation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errPermanent = errors.New("permanent failure")

// ids builds n distinct ids id00000..id{n-1}.
func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("id%05d", i)
	}
	return out
}

// poisonSink fails permanently iff a batch contains a poison id, and
// logs every batch it receives (comma-joined).
func poisonSink(poison map[string]bool, log *[]string) WriteFunc {
	return func(ids []string) error {
		if log != nil {
			*log = append(*log, strings.Join(ids, ","))
		}
		for _, id := range ids {
			if poison[id] {
				return errPermanent
			}
		}
		return nil
	}
}

func mustNew(t *testing.T, r, km, cmax int, sink WriteFunc) *Isolator {
	t.Helper()
	iso, err := New(r, km, cmax, sink)
	if err != nil {
		t.Fatalf("New(%d, %d, %d): %v", r, km, cmax, err)
	}
	return iso
}

func mustSubmit(t *testing.T, iso *Isolator, ids []string) Result {
	t.Helper()
	res, err := iso.Submit(ids)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return res
}

// deadStrings renders a dead list as "id/Reason" strings for comparison.
func deadStrings(dead []DeadLetter) []string {
	out := make([]string, len(dead))
	for i, d := range dead {
		out[i] = fmt.Sprintf("%s/%s", d.ID, d.Reason)
	}
	return out
}

func checkResult(t *testing.T, res Result, calls int, delivered, dead []string) {
	t.Helper()
	if res.Calls != calls {
		t.Errorf("Calls = %d, want %d", res.Calls, calls)
	}
	if !reflect.DeepEqual(res.Delivered, delivered) &&
		!(len(res.Delivered) == 0 && len(delivered) == 0) {
		t.Errorf("Delivered = %v, want %v", res.Delivered, delivered)
	}
	got := deadStrings(res.Dead)
	if !reflect.DeepEqual(got, dead) && !(len(got) == 0 && len(dead) == 0) {
		t.Errorf("Dead = %v, want %v", got, dead)
	}
}

// TestSpecExample replays the worked example: R=1, a0..a4, only a3 is a
// poison pill. The right half [a3,a4] must be inferred (certain) and
// never called as a whole batch, so Calls == 4 (not 5).
func TestSpecExample(t *testing.T) {
	var log []string
	iso := mustNew(t, 1, 100, 100, poisonSink(map[string]bool{"a3": true}, &log))

	res := mustSubmit(t, iso, []string{"a0", "a1", "a2", "a3", "a4"})
	checkResult(t, res, 4, []string{"a0", "a1", "a2", "a4"}, []string{"a3/Poison"})

	wantLog := []string{"a0,a1,a2,a3,a4", "a0,a1,a2", "a3", "a4"}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("sink call sequence = %v, want %v", log, wantLog)
	}
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"a3"}) {
		t.Errorf("Known() = %v, want [a3]", got)
	}

	// Second submit over the same table: a3 is filtered as Known and the
	// rest succeeds in a single call.
	log = nil
	res = mustSubmit(t, iso, []string{"a3", "b", "c"})
	checkResult(t, res, 1, []string{"b", "c"}, []string{"a3/Known"})
	if !reflect.DeepEqual(log, []string{"b,c"}) {
		t.Errorf("sink call sequence = %v, want [b,c]", log)
	}
}

// TestBudgetAbort checks that the Submit aborts exactly when the
// (Cmax+1)-th call would be needed, keeping adjudicated results, and
// that poisons confirmed before the abort still enter the table.
func TestBudgetAbort(t *testing.T) {
	// Cmax=3: the 4th call ([a4]) is not made; a4 is Budget.
	iso := mustNew(t, 1, 100, 3, poisonSink(map[string]bool{"a3": true}, nil))
	res := mustSubmit(t, iso, []string{"a0", "a1", "a2", "a3", "a4"})
	checkResult(t, res, 3, []string{"a0", "a1", "a2"},
		[]string{"a3/Poison", "a4/Budget"})
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"a3"}) {
		t.Errorf("Known() = %v, want [a3] (confirmed poison still recorded)", got)
	}

	// Cmax=2: both a3 and a4 fall to Budget; nothing new is recorded.
	iso = mustNew(t, 1, 100, 2, poisonSink(map[string]bool{"a3": true}, nil))
	res = mustSubmit(t, iso, []string{"a0", "a1", "a2", "a3", "a4"})
	checkResult(t, res, 2, []string{"a0", "a1", "a2"},
		[]string{"a3/Budget", "a4/Budget"})
	if got := iso.Known(); len(got) != 0 {
		t.Errorf("Known() = %v, want empty", got)
	}
}

// TestCeilLeftHalf pins the split rule: the left half takes ceil(n/2).
// For n=3 with the poison rightmost, ceil gives 2 sink calls while a
// floor split would need 3.
func TestCeilLeftHalf(t *testing.T) {
	var log []string
	iso := mustNew(t, 0, 100, 100, poisonSink(map[string]bool{"a2": true}, &log))
	res := mustSubmit(t, iso, []string{"a0", "a1", "a2"})
	checkResult(t, res, 2, []string{"a0", "a1"}, []string{"a2/Poison"})

	// Root call, then left half [a0,a1] (size 2 == ceil(3/2)), then the
	// right half [a2] is certain and never called.
	wantLog := []string{"a0,a1,a2", "a0,a1"}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("sink call sequence = %v, want %v", log, wantLog)
	}
}

// TestLeftSuccessAfterTransientRetries: the left half succeeds only
// after transient retries; lok is still true, so the right half of a
// permanently failed parent is still inferred certain.
func TestLeftSuccessAfterTransientRetries(t *testing.T) {
	var log []string
	abAttempts := 0
	sink := func(ids []string) error {
		key := strings.Join(ids, ",")
		log = append(log, key)
		for _, id := range ids {
			if id == "c" {
				return errPermanent
			}
		}
		if key == "a,b" && abAttempts == 0 {
			abAttempts++
			return ErrTransient
		}
		return nil
	}
	iso := mustNew(t, 1, 100, 100, sink)
	res := mustSubmit(t, iso, []string{"a", "b", "c", "d"})
	checkResult(t, res, 5, []string{"a", "b", "d"}, []string{"c/Poison"})

	// [c,d] must never be called as a whole: the parent failed
	// permanently and [a,b] eventually succeeded.
	wantLog := []string{"a,b,c,d", "a,b", "a,b", "c", "d"}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("sink call sequence = %v, want %v", log, wantLog)
	}
}

// TestTransientExhaustion: R+1 transient failures make perm=false, so
// children are called without inference; a singleton that exhausts
// transient retries is dead-lettered as Exhausted and not recorded.
func TestTransientExhaustion(t *testing.T) {
	var log []string
	xyAttempts := 0
	sink := func(ids []string) error {
		key := strings.Join(ids, ",")
		log = append(log, key)
		if key == "x,y" {
			xyAttempts++
			if xyAttempts <= 2 { // R+1 == 2 transient failures
				return ErrTransient
			}
		}
		return nil
	}
	iso := mustNew(t, 1, 100, 100, sink)
	res := mustSubmit(t, iso, []string{"x", "y"})
	checkResult(t, res, 4, []string{"x", "y"}, nil)
	wantLog := []string{"x,y", "x,y", "x", "y"}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("sink call sequence = %v, want %v", log, wantLog)
	}
	if got := iso.Known(); len(got) != 0 {
		t.Errorf("Known() = %v, want empty", got)
	}

	// Singleton transient exhaustion: Exhausted, not recorded.
	iso = mustNew(t, 0, 100, 100, func(ids []string) error { return ErrTransient })
	res = mustSubmit(t, iso, []string{"z"})
	checkResult(t, res, 1, nil, []string{"z/Exhausted"})
	if got := iso.Known(); len(got) != 0 {
		t.Errorf("Known() = %v, want empty (Exhausted is not recorded)", got)
	}
}

// TestPoisonRecordedDirectAndInferred: both a directly confirmed poison
// (singleton permanent failure) and an inferred one (certain, never
// called) enter the known table.
func TestPoisonRecordedDirectAndInferred(t *testing.T) {
	iso := mustNew(t, 0, 100, 100, poisonSink(map[string]bool{"p": true}, nil))
	res := mustSubmit(t, iso, []string{"p"})
	checkResult(t, res, 1, nil, []string{"p/Poison"})
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"p"}) {
		t.Errorf("Known() = %v, want [p]", got)
	}

	// Inferred: [q] is certain after [a,q] fails permanently and [a]
	// succeeds, so the sink never sees [q] alone.
	var log []string
	iso = mustNew(t, 0, 100, 100, poisonSink(map[string]bool{"q": true}, &log))
	res = mustSubmit(t, iso, []string{"a", "q"})
	checkResult(t, res, 2, []string{"a"}, []string{"q/Poison"})
	if !reflect.DeepEqual(log, []string{"a,q", "a"}) {
		t.Errorf("sink call sequence = %v, want [a,q a]", log)
	}
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"q"}) {
		t.Errorf("Known() = %v, want [q]", got)
	}
}

// TestTableEviction: a full table evicts the oldest appended entry;
// filtering an id as Known does not refresh its position.
func TestTableEviction(t *testing.T) {
	poison := map[string]bool{"p1": true, "p2": true, "p3": true}
	iso := mustNew(t, 0, 2, 100, poisonSink(poison, nil))

	mustSubmit(t, iso, []string{"p1"})
	mustSubmit(t, iso, []string{"p2"})
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Fatalf("Known() = %v, want [p1 p2]", got)
	}

	// p1 is filtered as Known (position not refreshed); p3 is confirmed
	// and appended, evicting p1 (the oldest).
	res := mustSubmit(t, iso, []string{"p1", "p3"})
	checkResult(t, res, 1, nil, []string{"p1/Known", "p3/Poison"})
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"p2", "p3"}) {
		t.Errorf("Known() = %v, want [p2 p3]", got)
	}
}

// TestKmZero: with Km=0 nothing is recorded.
func TestKmZero(t *testing.T) {
	iso := mustNew(t, 0, 0, 100, poisonSink(map[string]bool{"p": true}, nil))
	res := mustSubmit(t, iso, []string{"p"})
	checkResult(t, res, 1, nil, []string{"p/Poison"})
	if got := iso.Known(); len(got) != 0 {
		t.Errorf("Known() = %v, want empty with Km=0", got)
	}
}

// TestKnownFilteredEmptyBatch: when every id is filtered as Known, no
// sink call is made.
func TestKnownFilteredEmptyBatch(t *testing.T) {
	var log []string
	iso := mustNew(t, 0, 100, 100, poisonSink(map[string]bool{"p": true}, &log))
	mustSubmit(t, iso, []string{"p"})
	log = nil
	res := mustSubmit(t, iso, []string{"p"})
	checkResult(t, res, 0, nil, []string{"p/Known"})
	if len(log) != 0 {
		t.Errorf("sink called %v, want no calls", log)
	}
}

// TestConcurrentSubmitsDoNotSeeEachOther: two in-flight Submits over the
// same would-be poison both adjudicate it themselves (neither sees the
// other's not-yet-merged table entry); the table ends with one entry.
func TestConcurrentSubmitsDoNotSeeEachOther(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var sinkCalls int32
	sink := func(ids []string) error {
		atomic.AddInt32(&sinkCalls, 1)
		entered <- struct{}{}
		<-release
		return errPermanent
	}
	iso := mustNew(t, 0, 10, 100, sink)

	var wg sync.WaitGroup
	results := make([]Result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := iso.Submit([]string{"p"})
			if err != nil {
				t.Errorf("Submit: %v", err)
				return
			}
			results[i] = res
		}(i)
	}
	go func() {
		<-entered
		<-entered
		close(release)
	}()
	wg.Wait()

	for i, res := range results {
		if res.Calls != 1 || len(res.Dead) != 1 || res.Dead[0].Reason != ReasonPoison {
			t.Errorf("submit %d = %+v, want one Poison dead letter and 1 call", i, res)
		}
	}
	if got := iso.Known(); !reflect.DeepEqual(got, []string{"p"}) {
		t.Errorf("Known() = %v, want [p] (single entry, no duplicate)", got)
	}
}

// TestCallCounts1024 pins the exact call counts for n=1024 and checks
// the bound calls <= 1 + 2*k*ceil(log2 n) for several poison layouts.
func TestCallCounts1024(t *testing.T) {
	const n = 1024
	batch := ids(n)

	// Single poison at the leftmost position: exactly 21 calls.
	iso := mustNew(t, 3, 100, 1_000_000, poisonSink(map[string]bool{batch[0]: true}, nil))
	res := mustSubmit(t, iso, batch)
	if res.Calls != 21 {
		t.Errorf("leftmost poison: Calls = %d, want 21", res.Calls)
	}

	// Single poison at the rightmost position: exactly 11 calls.
	iso = mustNew(t, 3, 100, 1_000_000, poisonSink(map[string]bool{batch[n-1]: true}, nil))
	res = mustSubmit(t, iso, batch)
	if res.Calls != 11 {
		t.Errorf("rightmost poison: Calls = %d, want 11", res.Calls)
	}

	ceilLog2 := func(n int) int {
		l := 0
		for (1 << l) < n {
			l++
		}
		return l
	}
	bound := func(k int) int { return 1 + 2*k*ceilLog2(n) }

	// k poisons packed at the leftmost positions (worst case).
	for _, k := range []int{1, 2, 3, 5, 10} {
		poison := map[string]bool{}
		for i := 0; i < k; i++ {
			poison[batch[i]] = true
		}
		iso := mustNew(t, 0, 100, 1_000_000, poisonSink(poison, nil))
		res := mustSubmit(t, iso, batch)
		if res.Calls > bound(k) {
			t.Errorf("k=%d leftmost: Calls = %d, bound = %d", k, res.Calls, bound(k))
		}
		if len(res.Dead) != k || len(res.Delivered) != n-k {
			t.Errorf("k=%d leftmost: delivered=%d dead=%d, want %d/%d",
				k, len(res.Delivered), len(res.Dead), n-k, k)
		}
	}

	// k poisons at spread-out positions.
	for _, k := range []int{1, 4, 16} {
		poison := map[string]bool{}
		for i := 0; i < k; i++ {
			poison[batch[i*n/k]] = true
		}
		iso := mustNew(t, 0, 100, 1_000_000, poisonSink(poison, nil))
		res := mustSubmit(t, iso, batch)
		if res.Calls > bound(k) {
			t.Errorf("k=%d spread: Calls = %d, bound = %d", k, res.Calls, bound(k))
		}
	}
}

// TestRejectedOperations: invalid constructor parameters, malformed
// Submit arguments, and Submit after Close are all rejected without
// touching the sink or the known table. Invalid argument is reported
// before closed.
func TestRejectedOperations(t *testing.T) {
	okSink := func([]string) error { return nil }
	for _, args := range [][3]int{
		{-1, 1, 1}, {11, 1, 1}, // R out of [0,10]
		{1, -1, 1}, {1, 1001, 1}, // Km out of [0,1000]
		{1, 1, 0}, {1, 1, 1_000_001}, // Cmax out of [1,1e6]
	} {
		if _, err := New(args[0], args[1], args[2], okSink); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("New%v = %v, want ErrInvalidArgument", args, err)
		}
	}
	if _, err := New(0, 0, 1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("New with nil sink = %v, want ErrInvalidArgument", err)
	}

	var sinkCalls int32
	sink := func(ids []string) error {
		atomic.AddInt32(&sinkCalls, 1)
		return errPermanent
	}
	iso := mustNew(t, 0, 10, 100, sink)
	mustSubmit(t, iso, []string{"p"}) // establish table state [p]
	knownBefore := iso.Known()
	callsBefore := atomic.LoadInt32(&sinkCalls)

	tooLong := make([]string, 100_001)
	for i := range tooLong {
		tooLong[i] = fmt.Sprintf("x%d", i)
	}
	badInputs := map[string][]string{
		"nil":       nil,
		"empty":     {},
		"duplicate": {"a", "b", "a"},
		"empty id":  {"a", ""},
		"too long":  tooLong,
	}
	for name, in := range badInputs {
		if _, err := iso.Submit(in); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Submit(%s) = %v, want ErrInvalidArgument", name, err)
		}
	}
	if got := atomic.LoadInt32(&sinkCalls); got != callsBefore {
		t.Errorf("rejected Submits made %d sink calls", got-callsBefore)
	}
	if got := iso.Known(); !reflect.DeepEqual(got, knownBefore) {
		t.Errorf("rejected Submits changed table: %v -> %v", knownBefore, got)
	}

	if err := iso.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := iso.Submit([]string{"a"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Submit after Close = %v, want ErrClosed", err)
	}
	// Both invalid and closed: the invalid-argument reason comes first.
	if _, err := iso.Submit(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Submit(invalid) after Close = %v, want ErrInvalidArgument", err)
	}
	if got := atomic.LoadInt32(&sinkCalls); got != callsBefore {
		t.Errorf("rejected Submits made %d sink calls", got-callsBefore)
	}
	if got := iso.Known(); !reflect.DeepEqual(got, knownBefore) {
		t.Errorf("rejected Submits changed table: %v -> %v", knownBefore, got)
	}
}

// TestCloseWaitsAndIdempotent: Close blocks until in-flight Submits
// finish and can be called repeatedly.
func TestCloseWaitsAndIdempotent(t *testing.T) {
	inSink := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sink := func(ids []string) error {
		once.Do(func() { close(inSink) })
		<-release
		return nil
	}
	iso := mustNew(t, 0, 10, 10, sink)

	submitDone := make(chan struct{})
	go func() {
		defer close(submitDone)
		if _, err := iso.Submit([]string{"a"}); err != nil {
			t.Errorf("Submit: %v", err)
		}
	}()
	<-inSink

	closed := make(chan struct{})
	go func() {
		_ = iso.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a Submit was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-submitDone
	<-closed

	if err := iso.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}
}
