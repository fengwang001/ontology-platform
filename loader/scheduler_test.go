package loader

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

var (
	originA = Origin{Scheme: "https", Host: "a.example", Port: 443}
	originB = Origin{Scheme: "https", Host: "b.example", Port: 443}
	originC = Origin{Scheme: "http", Host: "a.example", Port: 80}
)

func mustSched(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func reg(t *testing.T, s *Scheduler, at int64, in RequestInput) uint64 {
	t.Helper()
	id, err := s.Register(at, in)
	if err != nil {
		t.Fatalf("Register(%s %s): %v", in.Type, in.URL, err)
	}
	t.Logf("register id=%d type=%s url=%s prio=%s", id, in.Type, in.URL, in.Priority)
	return id
}

func wantState(t *testing.T, s *Scheduler, id uint64, state State) Status {
	t.Helper()
	st, err := s.Status(id)
	if err != nil {
		t.Fatalf("Status(%d): %v", id, err)
	}
	if st.State != state {
		t.Fatalf("id=%d: state=%s, want %s", id, st.State, state)
	}
	t.Logf("id=%d state=%s progress=%d/%d pauses=%d (as expected)", id, st.State, st.Progress, st.Size, st.Pauses)
	return st
}

func script(origin Origin, url string, prio Priority, size int64) RequestInput {
	return RequestInput{Origin: origin, URL: url, Type: TypeScript, Priority: prio, Size: size}
}

func preload(origin Origin, url string, as ResourceType, prio Priority, size int64) RequestInput {
	return RequestInput{Origin: origin, URL: url, Type: TypePreload, As: as, Priority: prio, Size: size}
}

// Per-origin limit exactly full and one short.
func TestPerOriginLimitFullAndOneShort(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 10, MaxPauses: 3, PreloadTTL: 100})
	a1 := reg(t, s, 0, script(originA, "/1", PriorityMedium, 5))
	a2 := reg(t, s, 0, script(originA, "/2", PriorityMedium, 5))
	a3 := reg(t, s, 0, script(originA, "/3", PriorityMedium, 5))
	b1 := reg(t, s, 0, script(originB, "/4", PriorityMedium, 5))

	wantState(t, s, a1, StateTransferring)
	wantState(t, s, a2, StateTransferring)
	wantState(t, s, a3, StatePending) // origin A quota exactly full
	wantState(t, s, b1, StateTransferring)

	perOrigin, total := s.Stats()
	if perOrigin[originA] != 2 || perOrigin[originB] != 1 || total != 3 {
		t.Fatalf("stats=%v total=%d, want A=2 B=1 total=3", perOrigin, total)
	}
	t.Logf("verdict: origin A at limit 2/2 (full), origin B at 1/2 (one short)")
}

// Global limit binds before any per-origin limit.
func TestGlobalLimitBeforePerOrigin(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 5, GlobalLimit: 2, MaxPauses: 3, PreloadTTL: 100})
	a := reg(t, s, 0, script(originA, "/a", PriorityMedium, 5))
	b := reg(t, s, 0, script(originB, "/b", PriorityMedium, 5))
	c := reg(t, s, 0, script(originC, "/c", PriorityMedium, 5))

	wantState(t, s, a, StateTransferring)
	wantState(t, s, b, StateTransferring)
	wantState(t, s, c, StatePending) // global limit 2 reached, per-origin limit 5 untouched
	_, total := s.Stats()
	if total != 2 {
		t.Fatalf("total=%d, want 2", total)
	}
	t.Logf("verdict: global limit 2 hit first, per-origin counts all 1/5")
}

// Only highest-priority requests preempt.
func TestPreemptionOnlyByHighest(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 10, MaxPauses: 3, PreloadTTL: 100})
	low := reg(t, s, 0, script(originA, "/low", PriorityLow, 10))
	high := reg(t, s, 0, script(originA, "/high", PriorityHigh, 10))

	wantState(t, s, low, StateTransferring) // high (not highest) must not preempt
	wantState(t, s, high, StatePending)

	top := reg(t, s, 0, script(originA, "/top", PriorityHighest, 10))
	wantState(t, s, top, StateTransferring)
	st := wantState(t, s, low, StatePending) // paused by the highest request
	if st.Pauses != 1 {
		t.Fatalf("low pauses=%d, want 1", st.Pauses)
	}
	t.Logf("verdict: high did not preempt, highest did; victim paused once")
}

// A request paused MaxPauses times can no longer be preempted.
func TestPauseCapStopsPreemption(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 10, MaxPauses: 1, PreloadTTL: 100})
	low := reg(t, s, 0, script(originA, "/low", PriorityLow, 10))
	top1 := reg(t, s, 0, script(originA, "/top1", PriorityHighest, 2))
	if st := wantState(t, s, low, StatePending); st.Pauses != 1 {
		t.Fatalf("low pauses=%d, want 1", st.Pauses)
	}
	if err := s.Advance(top1, 2, 0); err != nil {
		t.Fatalf("advance top1: %v", err)
	}
	wantState(t, s, top1, StateCompleted)
	wantState(t, s, low, StateTransferring) // resumed

	top2 := reg(t, s, 0, script(originA, "/top2", PriorityHighest, 2))
	wantState(t, s, low, StateTransferring) // pause cap reached: not preemptable
	wantState(t, s, top2, StatePending)
	t.Logf("verdict: after 1/%d pauses the victim is immune, highest waits", 1)
}

// A paused request resumes from its progress, in registration order.
func TestResumeKeepsProgressAndRegistrationOrder(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 10, MaxPauses: 5, PreloadTTL: 100})
	a := reg(t, s, 0, script(originA, "/a", PriorityMedium, 10))
	if err := s.Advance(a, 4, 0); err != nil {
		t.Fatalf("advance a: %v", err)
	}
	b := reg(t, s, 0, script(originA, "/b", PriorityMedium, 5)) // pending behind a
	x := reg(t, s, 0, script(originA, "/x", PriorityHighest, 2))

	st := wantState(t, s, a, StatePending) // paused by x
	if st.Progress != 4 {
		t.Fatalf("a progress=%d after pause, want 4 (no rewind)", st.Progress)
	}
	if err := s.Advance(x, 2, 0); err != nil {
		t.Fatalf("advance x: %v", err)
	}
	// a was registered before b, so a resumes first despite being paused later.
	st = wantState(t, s, a, StateTransferring)
	if st.Progress != 4 {
		t.Fatalf("a progress=%d on resume, want 4", st.Progress)
	}
	wantState(t, s, b, StatePending)
	if err := s.Advance(a, 6, 0); err != nil {
		t.Fatalf("advance a: %v", err)
	}
	wantState(t, s, a, StateCompleted)
	wantState(t, s, b, StateTransferring)
	t.Logf("verdict: resume order follows registration seq, progress never regresses")
}

// withMeta sets credentials and integrity on a request.
func withMeta(in RequestInput, creds, integrity string) RequestInput {
	in.Credentials = creds
	in.Integrity = integrity
	return in
}

// completePreload registers and finishes a preload, returning its id.
func completePreload(t *testing.T, s *Scheduler, at int64, in RequestInput) uint64 {
	t.Helper()
	id := reg(t, s, at, in)
	if err := s.Advance(id, in.Size, at); err != nil {
		t.Fatalf("complete preload %d: %v", id, err)
	}
	wantState(t, s, id, StateCompleted)
	return id
}

// Cache hit requires address, type, credentials and integrity to all match.
func TestPreloadCacheFourConditions(t *testing.T) {
	cfg := Config{PerOriginLimit: 8, GlobalLimit: 16, MaxPauses: 3, PreloadTTL: 100}
	s := mustSched(t, cfg)
	pre := withMeta(preload(originA, "/u", TypeScript, PriorityLow, 2), "include", "h1")
	completePreload(t, s, 0, pre)
	if n := s.CacheSize(); n != 1 {
		t.Fatalf("cache size=%d, want 1", n)
	}

	// All four match: served from cache, entry consumed immediately.
	hit := reg(t, s, 0, withMeta(script(originA, "/u", PriorityMedium, 7), "include", "h1"))
	st := wantState(t, s, hit, StateCompleted)
	if !st.FromCache || st.Progress != 7 {
		t.Fatalf("hit: fromCache=%v progress=%d, want true/7", st.FromCache, st.Progress)
	}
	if n := s.CacheSize(); n != 0 {
		t.Fatalf("cache size=%d after use, want 0", n)
	}
	t.Logf("verdict: full match served from cache and consumed")

	// Re-preload the same key, then probe each mismatch in turn.
	completePreload(t, s, 0, pre)
	misses := []struct {
		name string
		in   RequestInput
	}{
		{"address mismatch", withMeta(script(originA, "/other", PriorityMedium, 3), "include", "h1")},
		{"type mismatch", withMeta(RequestInput{Origin: originA, URL: "/u", Type: TypeStyle, Priority: PriorityMedium, Size: 3}, "include", "h1")},
		{"credentials mismatch", withMeta(script(originA, "/u", PriorityMedium, 3), "omit", "h1")},
		{"integrity mismatch", withMeta(script(originA, "/u", PriorityMedium, 3), "include", "h2")},
		{"integrity empty vs set", withMeta(script(originA, "/u", PriorityMedium, 3), "include", "")},
	}
	for _, m := range misses {
		id := reg(t, s, 0, m.in)
		wantState(t, s, id, StateTransferring) // new transfer issued
		if n := s.CacheSize(); n != 1 {
			t.Fatalf("%s: cache size=%d, want 1 (entry untouched)", m.name, n)
		}
		t.Logf("verdict: %s -> new transfer, cache entry preserved", m.name)
	}

	// Both-empty integrity counts as equal.
	s2 := mustSched(t, cfg)
	completePreload(t, s2, 0, preload(originA, "/f", TypeFont, PriorityLow, 2))
	f := reg(t, s2, 0, RequestInput{Origin: originA, URL: "/f", Type: TypeFont, Priority: PriorityMedium, Size: 4})
	if st := wantState(t, s2, f, StateCompleted); !st.FromCache {
		t.Fatalf("empty==empty integrity should hit, fromCache=%v", st.FromCache)
	}
	t.Logf("verdict: both-empty integrity counts as equal")
}

// A cache entry is wasted only when unused strictly beyond the TTL.
func TestPreloadTTLExactBoundary(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 3, PreloadTTL: 5})
	completePreload(t, s, 10, preload(originA, "/u", TypeScript, PriorityLow, 2)) // completed at t=10

	if err := s.Tick(5); err != nil { // now=15, exactly TTL
		t.Fatalf("tick: %v", err)
	}
	if n := s.CacheSize(); n != 1 {
		t.Fatalf("at exactly TTL: cache size=%d, want 1 (not wasted yet)", n)
	}
	if w := s.Waste(); len(w) != 0 {
		t.Fatalf("at exactly TTL: waste=%v, want empty", w)
	}
	if err := s.Tick(1); err != nil { // now=16, beyond TTL
		t.Fatalf("tick: %v", err)
	}
	if n := s.CacheSize(); n != 0 {
		t.Fatalf("past TTL: cache size=%d, want 0", n)
	}
	w := s.Waste()
	if len(w) != 1 || w[0].CompletedAt != 10 || w[0].WastedAt != 16 || w[0].URL != "/u" {
		t.Fatalf("waste=%+v, want one entry completed=10 wasted=16", w)
	}
	t.Logf("verdict: alive at exactly TTL, wasted at TTL+1: %+v", w[0])
}

// Attachment to an in-flight preload raises its effective priority; a
// boost to highest triggers preemption for a paused preload.
func TestAttachmentBoostTriggersPreemption(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 10, MaxPauses: 3, PreloadTTL: 100})
	p := reg(t, s, 0, preload(originA, "/u", TypeScript, PriorityLow, 10))
	y := reg(t, s, 0, script(originA, "/y", PriorityMedium, 10))
	wantState(t, s, p, StateTransferring)
	wantState(t, s, y, StateTransferring) // origin quota 2/2 full

	x := reg(t, s, 0, script(originA, "/x", PriorityHighest, 2))
	wantState(t, s, p, StatePending) // lowest priority victim paused
	wantState(t, s, x, StateTransferring)

	// Highest-priority script with the same key attaches to the paused
	// preload; the boost to highest preempts the medium request y.
	h := reg(t, s, 0, script(originA, "/u", PriorityHighest, 10))
	st := wantState(t, s, h, StateAttached)
	if st.AttachedTo != p {
		t.Fatalf("h attachedTo=%d, want %d", st.AttachedTo, p)
	}
	wantState(t, s, p, StateTransferring) // resumed via attachment-driven preemption
	st = wantState(t, s, y, StatePending)
	if st.Pauses != 1 {
		t.Fatalf("y pauses=%d, want 1", st.Pauses)
	}
	if st := wantState(t, s, p, StateTransferring); st.EffectivePriority != PriorityHighest {
		t.Fatalf("p effective priority=%s, want highest", st.EffectivePriority)
	}

	if err := s.Advance(p, 10, 0); err != nil {
		t.Fatalf("advance p: %v", err)
	}
	wantState(t, s, p, StateCompleted)
	st = wantState(t, s, h, StateCompleted) // attacher shares completion
	if st.Progress != 10 {
		t.Fatalf("h progress=%d, want 10 (shared completion)", st.Progress)
	}
	t.Logf("verdict: attachment boosted preload to highest, preempted medium, shared completion")
}

// Aborting a preload fails its attachers with the aborted category.
func TestAbortPreloadCascadesToAttachers(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 3, PreloadTTL: 100})
	p := reg(t, s, 0, preload(originA, "/u", TypeScript, PriorityLow, 10))
	h := reg(t, s, 0, script(originA, "/u", PriorityHighest, 10))
	wantState(t, s, h, StateAttached)

	if err := s.Abort(p, 0); err != nil {
		t.Fatalf("abort p: %v", err)
	}
	wantState(t, s, p, StateAborted)
	st := wantState(t, s, h, StateAborted)
	if !errors.Is(st.Cause, ErrAborted) {
		t.Fatalf("h cause=%v, want ErrAborted", st.Cause)
	}
	t.Logf("verdict: attacher failed with category aborted")
}

// Terminal states are mutually exclusive and irreversible.
func TestTerminalStatesAreFinal(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 3, PreloadTTL: 100})
	a := reg(t, s, 0, script(originA, "/a", PriorityMedium, 2))
	if err := s.Advance(a, 2, 0); err != nil {
		t.Fatalf("advance a: %v", err)
	}
	wantState(t, s, a, StateCompleted)
	for _, op := range map[string]func() error{
		"advance": func() error { return s.Advance(a, 1, 0) },
		"abort":   func() error { return s.Abort(a, 0) },
		"fail":    func() error { return s.Fail(a, 0) },
	} {
		if err := op(); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("op on completed: err=%v, want ErrInvalidState", err)
		}
	}
	st := wantState(t, s, a, StateCompleted)
	if st.Progress != 2 {
		t.Fatalf("completed progress=%d, want 2 (stable)", st.Progress)
	}

	b := reg(t, s, 0, script(originA, "/b", PriorityMedium, 5))
	if err := s.Abort(b, 0); err != nil {
		t.Fatalf("abort b: %v", err)
	}
	if err := s.Abort(b, 0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("double abort: err=%v, want ErrInvalidState", err)
	}
	wantState(t, s, b, StateAborted)
	t.Logf("verdict: completed/aborted requests reject every further mutation")
}

// Rejection order: invalid argument, then clock skew, then not found,
// then state not allowed. Rejected operations change nothing.
func TestErrorOrderAndNoSideEffects(t *testing.T) {
	if _, err := NewScheduler(Config{PerOriginLimit: 0, GlobalLimit: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero per-origin limit: %v, want ErrInvalidArgument", err)
	}
	if _, err := NewScheduler(Config{PerOriginLimit: 1, GlobalLimit: 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero global limit: %v, want ErrInvalidArgument", err)
	}

	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 3, PreloadTTL: 100})
	base := reg(t, s, 5, script(originA, "/base", PriorityMedium, 5))
	if err := s.Advance(base, 2, 5); err != nil {
		t.Fatalf("advance base: %v", err)
	}
	done := reg(t, s, 5, script(originA, "/done", PriorityMedium, 1))
	if err := s.Advance(done, 1, 5); err != nil {
		t.Fatalf("advance done: %v", err)
	}

	snapshot := func() [4]int64 {
		_, total := s.Stats()
		st, _ := s.Status(base)
		return [4]int64{s.Now(), int64(total), int64(s.CacheSize()), st.Progress}
	}
	before := snapshot()

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty url beats clock skew", func() error {
			_, err := s.Register(0, script(originA, "", PriorityMedium, 1))
			return err
		}, ErrInvalidArgument},
		{"unknown type", func() error {
			in := script(originA, "/x", PriorityMedium, 1)
			in.Type = ResourceType(99)
			_, err := s.Register(5, in)
			return err
		}, ErrInvalidArgument},
		{"priority out of range", func() error {
			in := script(originA, "/x", Priority(9), 1)
			_, err := s.Register(5, in)
			return err
		}, ErrInvalidArgument},
		{"clock skew beats not found", func() error {
			return s.Abort(999, 4)
		}, ErrClockSkew},
		{"negative tick is clock skew", func() error { return s.Tick(-1) }, ErrClockSkew},
		{"not found", func() error { return s.Abort(999, 5) }, ErrNotFound},
		{"not found beats state", func() error { return s.Advance(999, 1, 5) }, ErrNotFound},
		{"state not allowed", func() error { return s.Abort(done, 5) }, ErrInvalidState},
		{"advance terminal", func() error { return s.Advance(done, 1, 5) }, ErrInvalidState},
	}
	for _, c := range cases {
		err := c.op()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v, want %v", c.name, err, c.want)
		}
		if after := snapshot(); after != before {
			t.Fatalf("%s: rejected op mutated state: before=%v after=%v", c.name, before, after)
		}
		t.Logf("verdict: %s rejected as %v with no side effects", c.name, c.want)
	}
}

// A second preload of the same address is rejected, never overwritten.
func TestDuplicatePreloadRejected(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 3, PreloadTTL: 100})
	in := preload(originA, "/u", TypeScript, PriorityLow, 2)
	p := reg(t, s, 0, in)
	if _, err := s.Register(0, in); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate active preload: %v, want ErrInvalidArgument", err)
	}
	if err := s.Advance(p, 2, 0); err != nil { // now cached; duplicate still rejected
		t.Fatalf("complete preload: %v", err)
	}
	if _, err := s.Register(0, in); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate cached preload: %v, want ErrInvalidArgument", err)
	}
	// Consume the cache entry; re-registration becomes legal again.
	hit := reg(t, s, 0, script(originA, "/u", PriorityMedium, 3))
	if st := wantState(t, s, hit, StateCompleted); !st.FromCache {
		t.Fatalf("expected cache hit to consume the entry")
	}
	if _, err := s.Register(0, in); err != nil {
		t.Fatalf("preload after consumption should be legal: %v", err)
	}
	t.Logf("verdict: duplicate preload rejected while active or cached, allowed after use")
}

// Concurrent register/advance/abort/tick calls serialize correctly.
func TestConcurrentOps(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 2, PreloadTTL: 50})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var ids []uint64
			for i := 0; i < 100; i++ {
				now := s.Now()
				switch i % 4 {
				case 0:
					in := script(originA, fmt.Sprintf("/g%d/%d", g, i), Priority(i%5), 3)
					if id, err := s.Register(now, in); err == nil {
						ids = append(ids, id)
					}
				case 1:
					if len(ids) > 0 {
						_ = s.Advance(ids[len(ids)-1], 1, now)
					}
				case 2:
					if len(ids) > 0 {
						_ = s.Abort(ids[0], now)
						ids = ids[1:]
					}
				case 3:
					_ = s.Tick(1)
				}
			}
		}(g)
	}
	wg.Wait()

	perOrigin, total := s.Stats()
	for o, n := range perOrigin {
		if n > 2 {
			t.Fatalf("origin %v in-flight=%d exceeds per-origin limit 2", o, n)
		}
	}
	if total > 4 {
		t.Fatalf("total in-flight=%d exceeds global limit 4", total)
	}
	t.Logf("verdict: after concurrent storm, per-origin=%v total=%d within limits", perOrigin, total)
}

// Benchmark: selecting the next startable request must not depend on the
// number of pending requests.
func BenchmarkSelectNext(b *testing.B) {
	for _, n := range []int{100, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("pending=%d", n), func(b *testing.B) {
			s, err := NewScheduler(Config{PerOriginLimit: 1 << 30, GlobalLimit: 1, MaxPauses: 3, PreloadTTL: 1 << 60})
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				in := script(originA, fmt.Sprintf("/%d", i), Priority(i%numPriorities), 1)
				if _, err := s.Register(0, in); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if s.pending.peek() == nil {
					b.Fatal("expected a pending request")
				}
			}
		})
	}
}

// Benchmark: preload cache hit must not depend on the number of entries.
func BenchmarkCacheHit(b *testing.B) {
	for _, n := range []int{1, 100, 10000, 100000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			c := newPreloadCache(1 << 60)
			var probe RequestInput
			for i := 0; i < n; i++ {
				key := cacheKey{originA, fmt.Sprintf("/%d", i)}
				c.put(key, &cacheEntry{as: TypeScript, credentials: "include", integrity: "h"})
				probe = RequestInput{Origin: originA, URL: "/0", Type: TypeScript, Credentials: "include", Integrity: "h"}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !c.hit(probe) {
					b.Fatal("expected hit")
				}
			}
		})
	}
}
