package loader

import (
	"fmt"
	"testing"
)

func mustSched(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func org(host string) Origin { return Origin{Scheme: "https", Host: host, Port: 443} }

func reqIn(host, url string, typ ResourceType, prio Priority) RequestInput {
	return RequestInput{
		Origin:      org(host),
		URL:         url,
		Type:        typ,
		Priority:    prio,
		Credentials: CredentialsSameOrigin,
		Size:        10,
	}
}

func mustRegister(t *testing.T, s *Scheduler, at int64, in RequestInput) uint64 {
	t.Helper()
	id, err := s.Register(at, in)
	if err != nil {
		t.Fatalf("Register(%s): %v", in.URL, err)
	}
	return id
}

func mustState(t *testing.T, s *Scheduler, id uint64, want State) Snapshot {
	t.Helper()
	snap, err := s.Query(id)
	if err != nil {
		t.Fatalf("Query(%d): %v", id, err)
	}
	if snap.State != want {
		t.Fatalf("request %d: state=%s, want %s", id, snap.State, want)
	}
	return snap
}

func mustErrKind(t *testing.T, op string, err error, want ErrorKind) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("%s: err=%v, want kind %s", op, err, want)
	}
	if e.Kind != want {
		t.Fatalf("%s: kind=%s, want %s", op, e.Kind, want)
	}
	t.Logf("op=%s out=rejected why=%s", op, e.Kind)
}

type recorder struct{ evs []Event }

func (r *recorder) add(e Event) { r.evs = append(r.evs, e) }

func (r *recorder) kinds() []EventKind {
	out := make([]EventKind, len(r.evs))
	for i, e := range r.evs {
		out[i] = e.Kind
	}
	return out
}

func (r *recorder) count(kind EventKind) int {
	n := 0
	for _, e := range r.evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestPerOriginLimitFullAndMinusOne(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 10, MaxPauses: 3, PreloadTTL: 100})
	rec := &recorder{}
	s.Subscribe(rec.add)

	mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityMedium))
	st := s.Stats()
	t.Logf("in=register#1 out=in-flight why=per-origin 1 < limit 2 (差一)")
	if st.InFlightPerOrigin[org("a")] != 1 {
		t.Fatalf("per-origin=%d, want 1 (limit-1)", st.InFlightPerOrigin[org("a")])
	}

	mustRegister(t, s, 2, reqIn("a", "/2", TypeScript, PriorityMedium))
	st = s.Stats()
	t.Logf("in=register#2 out=in-flight why=per-origin 2 == limit 2 (打满)")
	if st.InFlightPerOrigin[org("a")] != 2 {
		t.Fatalf("per-origin=%d, want 2 (exactly full)", st.InFlightPerOrigin[org("a")])
	}

	id3 := mustRegister(t, s, 3, reqIn("a", "/3", TypeScript, PriorityMedium))
	mustState(t, s, id3, StatePending)
	t.Logf("in=register#3 out=pending why=per-origin quota exhausted")
	if rec.count(EventStarted) != 2 {
		t.Fatalf("started=%d, want 2", rec.count(EventStarted))
	}
}

func TestGlobalLimitBindsBeforePerOrigin(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 5, GlobalLimit: 2, MaxPauses: 3, PreloadTTL: 100})
	mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityMedium))
	mustRegister(t, s, 2, reqIn("b", "/2", TypeScript, PriorityMedium))
	id3 := mustRegister(t, s, 3, reqIn("c", "/3", TypeScript, PriorityMedium))
	st := s.Stats()
	t.Logf("in=3 requests on 3 origins out=2 in-flight why=global limit 2 < per-origin 5")
	if st.InFlightTotal != 2 {
		t.Fatalf("in-flight total=%d, want 2", st.InFlightTotal)
	}
	mustState(t, s, id3, StatePending)
}

func TestPreemptionOnlyByHighest(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 5, PreloadTTL: 100})
	rec := &recorder{}
	s.Subscribe(rec.add)

	r1 := mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityMedium))
	mustState(t, s, r1, StateInFlight)

	for i, p := range []Priority{PriorityLowest, PriorityLow, PriorityMedium, PriorityHigh} {
		id := mustRegister(t, s, int64(10+i), reqIn("a", fmt.Sprintf("/p%d", i), TypeScript, p))
		mustState(t, s, id, StatePending)
		t.Logf("in=register prio=%s out=pending why=only highest preempts", p)
	}
	if rec.count(EventPaused) != 0 {
		t.Fatalf("paused events=%d, want 0", rec.count(EventPaused))
	}

	h := mustRegister(t, s, 20, reqIn("a", "/h", TypeScript, PriorityHighest))
	mustState(t, s, h, StateInFlight)
	snap := mustState(t, s, r1, StatePending)
	t.Logf("in=register prio=highest out=preempt why=highest displaces lowest-prio in-flight of same origin")
	if snap.Pauses != 1 {
		t.Fatalf("r1 pauses=%d, want 1", snap.Pauses)
	}
	if rec.count(EventPaused) != 1 {
		t.Fatalf("paused events=%d, want 1", rec.count(EventPaused))
	}
}

func TestPauseLimitReachedStopsPreemption(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 1, PreloadTTL: 100})
	r1 := mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityLow))
	h1 := mustRegister(t, s, 2, reqIn("a", "/h1", TypeScript, PriorityHighest))
	mustState(t, s, r1, StatePending)
	t.Logf("in=highest#1 out=r1 paused why=pauses 0 < maxPauses 1")

	if err := s.Progress(3, h1, 10); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	mustState(t, s, r1, StateInFlight)
	t.Logf("in=complete h1 out=r1 resumed why=quota freed")

	h2 := mustRegister(t, s, 4, reqIn("a", "/h2", TypeScript, PriorityHighest))
	mustState(t, s, h2, StatePending)
	snap := mustState(t, s, r1, StateInFlight)
	t.Logf("in=highest#2 out=no preemption why=r1 pauses==maxPauses, not pausable")
	if snap.Pauses != 1 {
		t.Fatalf("r1 pauses=%d, want 1", snap.Pauses)
	}
}

func TestResumeKeepsProgressAndRegistrationOrder(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 5, PreloadTTL: 100})
	rec := &recorder{}
	s.Subscribe(rec.add)

	r1 := mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityHigh))
	if err := s.Progress(2, r1, 4); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	r2 := mustRegister(t, s, 3, reqIn("a", "/2", TypeScript, PriorityHigh))
	mustState(t, s, r2, StatePending)

	h := mustRegister(t, s, 4, reqIn("a", "/h", TypeScript, PriorityHighest))
	snap := mustState(t, s, r1, StatePending)
	if snap.Progress != 4 {
		t.Fatalf("r1 progress=%d after pause, want 4 (no rewind)", snap.Progress)
	}
	t.Logf("in=highest out=r1 paused at progress 4 why=preemption keeps progress")

	if err := s.Progress(5, h, 10); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	snap = mustState(t, s, r1, StateInFlight)
	if snap.Progress != 4 {
		t.Fatalf("r1 progress=%d after resume, want 4", snap.Progress)
	}
	mustState(t, s, r2, StatePending)
	t.Logf("in=complete h out=r1 resumed before r2 why=original registration order")

	var resumedIdx, startedR2Idx int = -1, -1
	for i, e := range rec.evs {
		if e.Kind == EventResumed && e.ID == r1 {
			resumedIdx = i
		}
		if e.Kind == EventStarted && e.ID == r2 {
			startedR2Idx = i
		}
	}
	if resumedIdx < 0 {
		t.Fatalf("no resumed event for r1")
	}
	if err := s.Progress(6, r1, 6); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	mustState(t, s, r1, StateDone)
	mustState(t, s, r2, StateInFlight)
	for i, e := range rec.evs {
		if e.Kind == EventStarted && e.ID == r2 {
			startedR2Idx = i
		}
	}
	if startedR2Idx < 0 || startedR2Idx < resumedIdx {
		t.Fatalf("r2 started before r1 resumed")
	}
}

func preloadIn(host, url string, as ResourceType, prio Priority) RequestInput {
	in := reqIn(host, url, TypePreload, prio)
	in.As = as
	in.Credentials = CredentialsInclude
	in.Integrity = "sha256-x"
	in.Size = 1
	return in
}

func TestPreloadCacheMatchConditions(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 8, GlobalLimit: 16, MaxPauses: 3, PreloadTTL: 1000})
	rec := &recorder{}
	s.Subscribe(rec.add)

	p := mustRegister(t, s, 1, preloadIn("a", "/a.js", TypeScript, PriorityHigh))
	if err := s.Progress(2, p, 1); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	mustState(t, s, p, StateDone)
	if got := s.Stats().CacheSize; got != 1 {
		t.Fatalf("cache size=%d, want 1", got)
	}
	t.Logf("in=preload complete out=cache entry why=key (origin, /a.js)")

	cases := []struct {
		name string
		mut  func(*RequestInput)
	}{
		{"url differs", func(in *RequestInput) { in.URL = "/b.js" }},
		{"type differs", func(in *RequestInput) { in.Type = TypeStyle }},
		{"credentials differ", func(in *RequestInput) { in.Credentials = CredentialsOmit }},
		{"integrity differs", func(in *RequestInput) { in.Integrity = "sha256-y" }},
	}
	for i, c := range cases {
		in := reqIn("a", "/a.js", TypeScript, PriorityMedium)
		in.Credentials = CredentialsInclude
		in.Integrity = "sha256-x"
		c.mut(&in)
		id := mustRegister(t, s, int64(10+i), in)
		mustState(t, s, id, StateInFlight)
		if got := s.Stats().CacheSize; got != 1 {
			t.Fatalf("%s: cache size=%d, want 1 (miss must not evict)", c.name, got)
		}
		t.Logf("in=%s out=new transfer why=cache miss, entry untouched", c.name)
	}
	if rec.count(EventCacheHit) != 0 {
		t.Fatalf("cache hits=%d, want 0", rec.count(EventCacheHit))
	}

	hit := reqIn("a", "/a.js", TypeScript, PriorityMedium)
	hit.Credentials = CredentialsInclude
	hit.Integrity = "sha256-x"
	idHit := mustRegister(t, s, 20, hit)
	snap := mustState(t, s, idHit, StateDone)
	if !snap.FromCache {
		t.Fatalf("exact match: FromCache=false, want true")
	}
	if got := s.Stats().CacheSize; got != 0 {
		t.Fatalf("cache size=%d after use, want 0 (consumed)", got)
	}
	t.Logf("in=exact 4-field match out=done from cache why=hit consumes entry")

	idMiss := mustRegister(t, s, 21, hit)
	mustState(t, s, idMiss, StateInFlight)
	t.Logf("in=same request again out=new transfer why=entry already consumed")
}

func TestPreloadCacheEmptyIntegrityMatches(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 1, PreloadTTL: 100})
	in := preloadIn("a", "/f.woff2", TypeFont, PriorityHigh)
	in.Integrity = ""
	p := mustRegister(t, s, 1, in)
	if err := s.Progress(2, p, 1); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	hit := reqIn("a", "/f.woff2", TypeFont, PriorityLow)
	hit.Credentials = CredentialsInclude
	hit.Integrity = ""
	id := mustRegister(t, s, 3, hit)
	snap := mustState(t, s, id, StateDone)
	t.Logf("in=both integrity empty out=hit why=empty equals empty")
	if !snap.FromCache {
		t.Fatalf("FromCache=false, want true")
	}
}

func TestPreloadTTLBoundary(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 1, PreloadTTL: 10})
	p := mustRegister(t, s, 4, preloadIn("a", "/p.js", TypeScript, PriorityHigh))
	if err := s.Progress(5, p, 1); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if err := s.AdvanceClock(15); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	st := s.Stats()
	t.Logf("in=clock 15 (age 10 == TTL) out=alive why=wasted only when age > TTL")
	if st.CacheSize != 1 || st.Wasted != 0 {
		t.Fatalf("at TTL boundary: cache=%d wasted=%d, want 1/0", st.CacheSize, st.Wasted)
	}
	if err := s.AdvanceClock(16); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	st = s.Stats()
	t.Logf("in=clock 16 (age 11 > TTL) out=wasted why=unused past TTL")
	if st.CacheSize != 0 || st.Wasted != 1 {
		t.Fatalf("past TTL: cache=%d wasted=%d, want 0/1", st.CacheSize, st.Wasted)
	}
	w := s.Wasted()
	if len(w) != 1 || w[0].URL != "/p.js" || w[0].CompletedAt != 5 || w[0].WastedAt != 16 {
		t.Fatalf("waste record=%+v", w)
	}
}

func TestAttachUpgradeTriggersPreemption(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 5, MaxPauses: 5, PreloadTTL: 100})
	rec := &recorder{}
	s.Subscribe(rec.add)

	r1 := mustRegister(t, s, 1, reqIn("a", "/r1", TypeScript, PriorityLow))
	mustState(t, s, r1, StateInFlight)

	p := mustRegister(t, s, 2, preloadIn("a", "/x.js", TypeScript, PriorityLow))
	mustState(t, s, p, StatePending)
	t.Logf("in=preload low out=pending why=per-origin quota full")

	m := reqIn("a", "/x.js", TypeScript, PriorityHighest)
	m.Credentials = CredentialsInclude
	m.Integrity = "sha256-x"
	mID := mustRegister(t, s, 3, m)
	mustState(t, s, mID, StateAttached)
	mustState(t, s, p, StateInFlight)
	snap := mustState(t, s, r1, StatePending)
	t.Logf("in=attach highest out=preload upgraded, r1 paused why=effective priority rose to highest")
	if snap.Pauses != 1 {
		t.Fatalf("r1 pauses=%d, want 1", snap.Pauses)
	}
	if rec.count(EventAttached) != 1 || rec.count(EventPaused) != 1 {
		t.Fatalf("events attached=%d paused=%d", rec.count(EventAttached), rec.count(EventPaused))
	}

	if err := s.Progress(4, p, 1); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	snapM := mustState(t, s, mID, StateDone)
	if !snapM.FromCache {
		t.Fatalf("attacher FromCache=false, want true")
	}
	mustState(t, s, p, StateDone)
	t.Logf("in=preload completes out=attacher done from cache why=shared completion")
}

func TestAbortPreloadFailsAttachers(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 5, MaxPauses: 5, PreloadTTL: 100})
	p := mustRegister(t, s, 1, preloadIn("a", "/x.js", TypeScript, PriorityHigh))
	mustState(t, s, p, StateInFlight)

	m := reqIn("a", "/x.js", TypeScript, PriorityMedium)
	m.Credentials = CredentialsInclude
	m.Integrity = "sha256-x"
	mID := mustRegister(t, s, 2, m)
	mustState(t, s, mID, StateAttached)

	if err := s.Cancel(3, p); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	mustState(t, s, p, StateAborted)
	snap := mustState(t, s, mID, StateFailed)
	t.Logf("in=cancel preload out=attacher failed why=abort cascades with kind aborted")
	if !snap.HasErr || snap.ErrKind != ErrAborted {
		t.Fatalf("attacher err=(%v,%s), want (true,%s)", snap.HasErr, snap.ErrKind, ErrAborted)
	}
}

func TestTerminalStatesAreFinal(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 5, MaxPauses: 5, PreloadTTL: 100})
	done := mustRegister(t, s, 1, reqIn("a", "/d", TypeScript, PriorityMedium))
	if err := s.Progress(2, done, 10); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	mustState(t, s, done, StateDone)

	mustErrKind(t, "cancel(done)", s.Cancel(3, done), ErrIllegalState)
	mustErrKind(t, "progress(done)", s.Progress(3, done, 1), ErrIllegalState)
	mustErrKind(t, "fail(done)", s.Fail(3, done), ErrIllegalState)

	first, _ := s.Query(done)
	second, _ := s.Query(done)
	if first != second {
		t.Fatalf("terminal query unstable: %+v vs %+v", first, second)
	}
	t.Logf("in=query terminal twice out=identical why=terminal states irreversible")

	pend := mustRegister(t, s, 4, reqIn("b", "/p", TypeImage, PriorityLowest))
	if err := s.Cancel(5, pend); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	mustState(t, s, pend, StateAborted)
	mustErrKind(t, "cancel(aborted)", s.Cancel(6, pend), ErrIllegalState)

	inFlight := mustRegister(t, s, 7, reqIn("c", "/f", TypeImage, PriorityLow))
	if err := s.Fail(8, inFlight); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	mustState(t, s, inFlight, StateFailed)
	mustErrKind(t, "cancel(failed)", s.Cancel(9, inFlight), ErrIllegalState)
}

func TestRejectionPrecedenceAndPurity(t *testing.T) {
	if _, err := NewScheduler(Config{PerOriginLimit: 0, GlobalLimit: 1}); err == nil {
		t.Fatalf("zero per-origin limit accepted")
	} else {
		mustErrKind(t, "new(perOrigin=0)", err, ErrInvalidArgument)
	}
	if _, err := NewScheduler(Config{PerOriginLimit: 1, GlobalLimit: 0}); err == nil {
		t.Fatalf("zero global limit accepted")
	} else {
		mustErrKind(t, "new(global=0)", err, ErrInvalidArgument)
	}

	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 2, MaxPauses: 1, PreloadTTL: 10})
	id := mustRegister(t, s, 5, reqIn("a", "/x", TypeScript, PriorityMedium))
	if err := s.Progress(6, id, 10); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	bad := reqIn("a", "", TypeScript, PriorityMedium)
	mustErrKind(t, "register(empty url, rewound clock)", func() error {
		_, err := s.Register(1, bad)
		return err
	}(), ErrInvalidArgument)

	unknown := uint64(9999)
	mustErrKind(t, "cancel(unknown, rewound clock)", s.Cancel(1, unknown), ErrClockRewind)
	mustErrKind(t, "cancel(unknown)", s.Cancel(7, unknown), ErrNotFound)
	mustErrKind(t, "cancel(terminal)", s.Cancel(7, id), ErrIllegalState)
	mustErrKind(t, "progress(negative)", s.Progress(7, id, -1), ErrInvalidArgument)

	badType := reqIn("a", "/t", ResourceType(99), PriorityMedium)
	mustErrKind(t, "register(unknown type)", func() error {
		_, err := s.Register(7, badType)
		return err
	}(), ErrInvalidArgument)
	badPrio := reqIn("a", "/p", TypeScript, Priority(42))
	mustErrKind(t, "register(priority out of range)", func() error {
		_, err := s.Register(7, badPrio)
		return err
	}(), ErrInvalidArgument)

	if got := s.Stats().Now; got != 6 {
		t.Fatalf("clock=%d after rejected ops, want 6 (rejections are pure)", got)
	}
	t.Logf("in=rejected ops out=clock unchanged why=rejection must not mutate")
}

func TestDuplicatePreloadRejected(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 2, GlobalLimit: 5, MaxPauses: 1, PreloadTTL: 100})
	p1 := mustRegister(t, s, 1, preloadIn("a", "/dup.js", TypeScript, PriorityHigh))
	mustErrKind(t, "register(dup preload, in-flight)", func() error {
		_, err := s.Register(2, preloadIn("a", "/dup.js", TypeScript, PriorityHigh))
		return err
	}(), ErrInvalidArgument)

	if err := s.Progress(3, p1, 1); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	mustErrKind(t, "register(dup preload, cached)", func() error {
		_, err := s.Register(4, preloadIn("a", "/dup.js", TypeScript, PriorityHigh))
		return err
	}(), ErrInvalidArgument)
	t.Logf("in=second preload same key out=rejected why=duplicate preload is invalid argument")

	hit := reqIn("a", "/dup.js", TypeScript, PriorityMedium)
	hit.Credentials = CredentialsInclude
	hit.Integrity = "sha256-x"
	mustRegister(t, s, 5, hit)
	p2 := mustRegister(t, s, 6, preloadIn("a", "/dup.js", TypeScript, PriorityHigh))
	mustState(t, s, p2, StateInFlight)
	t.Logf("in=preload after consumption out=accepted why=cache entry was consumed")
}

func TestCancelInFlightBackfills(t *testing.T) {
	s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 1, MaxPauses: 1, PreloadTTL: 10})
	rec := &recorder{}
	s.Subscribe(rec.add)
	r1 := mustRegister(t, s, 1, reqIn("a", "/1", TypeScript, PriorityMedium))
	r2 := mustRegister(t, s, 2, reqIn("a", "/2", TypeScript, PriorityMedium))
	mustState(t, s, r2, StatePending)

	if err := s.Cancel(3, r1); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	mustState(t, s, r1, StateAborted)
	mustState(t, s, r2, StateInFlight)
	if got := s.Stats().InFlightTotal; got != 1 {
		t.Fatalf("in-flight=%d, want 1", got)
	}
	t.Logf("in=cancel in-flight out=pending started why=freed quota refilled")
}

func TestSelectionCostIndependentOfPending(t *testing.T) {
	measure := func(n int) int {
		s := mustSched(t, Config{PerOriginLimit: 1, GlobalLimit: 1, MaxPauses: 0, PreloadTTL: 10})
		hosts := []string{"a", "b", "c", "d"}
		var first uint64
		var at int64
		for i := 0; i < n; i++ {
			at++
			in := reqIn(hosts[i%len(hosts)], fmt.Sprintf("/r%d", i), TypeScript, PriorityMedium)
			id := mustRegister(t, s, at, in)
			if i == 0 {
				first = id
			}
		}
		at++
		if err := s.Progress(at, first, 10); err != nil {
			t.Fatalf("Progress: %v", err)
		}
		return s.pending.steps
	}
	small := measure(50)
	large := measure(20000)
	t.Logf("in=pending 50 vs 20000 out=steps %d vs %d why=selection scans origins x levels only", small, large)
	if small != large {
		t.Fatalf("selection steps grow with pending count: %d vs %d", small, large)
	}
	if large > 4*5 {
		t.Fatalf("selection steps=%d exceed origins x levels bound", large)
	}
}
