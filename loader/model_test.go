package loader

// A naive, independently written reference model of the scheduler. It uses
// plain slices and linear scans instead of the optimized queue and shares no
// code with Scheduler, so the randomized test below cross-checks the real
// implementation against an obviously-simple one.

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type mReq struct {
	id        uint64
	in        RequestInput
	as        ResourceType
	prio      Priority
	seq       uint64
	startSeq  uint64
	pauses    int
	progress  int64
	state     State
	fromCache bool
	hasErr    bool
	errKind   ErrorKind
	attachTo  uint64
	attachers []uint64
}

type model struct {
	cfg        Config
	now        int64
	seq        uint64
	startSeq   uint64
	nextID     uint64
	reqs       map[uint64]*mReq
	order      []uint64
	cache      map[cacheKey]*cacheEntry
	cacheOrder []cacheKey
	preloads   map[cacheKey]uint64
	inflight   map[uint64]bool
	wasted     []WasteRecord
}

func newModel(cfg Config) *model {
	return &model{
		cfg:      cfg,
		reqs:     make(map[uint64]*mReq),
		cache:    make(map[cacheKey]*cacheEntry),
		preloads: make(map[cacheKey]uint64),
		inflight: make(map[uint64]bool),
	}
}

func (m *model) event(kind EventKind, r *mReq) Event {
	return Event{
		Time:     m.now,
		Kind:     kind,
		ID:       r.id,
		Origin:   r.in.Origin,
		URL:      r.in.URL,
		Priority: r.prio,
		Progress: r.progress,
	}
}

func (m *model) perOrigin(o Origin) int {
	n := 0
	for id := range m.inflight {
		if m.reqs[id].in.Origin == o {
			n++
		}
	}
	return n
}

func (m *model) expire() []Event {
	var evs []Event
	for len(m.cacheOrder) > 0 {
		k := m.cacheOrder[0]
		e := m.cache[k]
		if e == nil {
			m.cacheOrder = m.cacheOrder[1:]
			continue
		}
		if m.now-e.completedAt <= m.cfg.PreloadTTL {
			break
		}
		delete(m.cache, k)
		m.cacheOrder = m.cacheOrder[1:]
		m.wasted = append(m.wasted, WasteRecord{
			Origin:      k.origin,
			URL:         k.url,
			Type:        e.as,
			CompletedAt: e.completedAt,
			WastedAt:    m.now,
		})
		evs = append(evs, Event{Time: m.now, Kind: EventWasted, Origin: k.origin, URL: k.url})
	}
	return evs
}

func (m *model) schedule() []Event {
	var evs []Event
	for len(m.inflight) < m.cfg.GlobalLimit {
		var best *mReq
		for _, id := range m.order {
			r := m.reqs[id]
			if r.state != StatePending {
				continue
			}
			if m.perOrigin(r.in.Origin) >= m.cfg.PerOriginLimit {
				continue
			}
			if best == nil || r.prio > best.prio || (r.prio == best.prio && r.seq < best.seq) {
				best = r
			}
		}
		if best == nil {
			break
		}
		best.state = StateInFlight
		m.startSeq++
		best.startSeq = m.startSeq
		m.inflight[best.id] = true
		kind := EventStarted
		if best.pauses > 0 {
			kind = EventResumed
		}
		evs = append(evs, m.event(kind, best))
		if best.in.Size == 0 {
			evs = append(evs, m.complete(best)...)
		}
	}
	return evs
}

func (m *model) preempt(r *mReq) []Event {
	if r.state != StatePending || r.prio != PriorityHighest {
		return nil
	}
	if len(m.inflight) < m.cfg.GlobalLimit && m.perOrigin(r.in.Origin) < m.cfg.PerOriginLimit {
		return nil
	}
	var victim *mReq
	for id := range m.inflight {
		c := m.reqs[id]
		if c.in.Origin != r.in.Origin || c.prio >= PriorityHighest || c.pauses >= m.cfg.MaxPauses {
			continue
		}
		if victim == nil || c.prio < victim.prio ||
			(c.prio == victim.prio && c.startSeq > victim.startSeq) {
			victim = c
		}
	}
	if victim == nil {
		return nil
	}
	victim.pauses++
	victim.state = StatePending
	delete(m.inflight, victim.id)
	return []Event{m.event(EventPaused, victim)}
}

func (m *model) complete(r *mReq) []Event {
	r.state = StateDone
	delete(m.inflight, r.id)
	var evs []Event
	if r.in.Type == TypePreload {
		k := cacheKey{origin: r.in.Origin, url: r.in.URL}
		delete(m.preloads, k)
		m.cache[k] = &cacheEntry{as: r.as, cred: r.in.Credentials, integrity: r.in.Integrity, completedAt: m.now}
		m.cacheOrder = append(m.cacheOrder, k)
	}
	for _, aid := range r.attachers {
		a := m.reqs[aid]
		a.state = StateDone
		a.fromCache = true
		a.attachTo = 0
		evs = append(evs, m.event(EventCompleted, a))
	}
	r.attachers = nil
	evs = append(evs, m.event(EventCompleted, r))
	return append(evs, m.schedule()...)
}

func (m *model) fail(r *mReq) []Event {
	r.state = StateFailed
	delete(m.inflight, r.id)
	var evs []Event
	if r.in.Type == TypePreload {
		delete(m.preloads, cacheKey{origin: r.in.Origin, url: r.in.URL})
	}
	for _, aid := range r.attachers {
		a := m.reqs[aid]
		a.state = StateFailed
		a.attachTo = 0
		evs = append(evs, m.event(EventFailed, a))
	}
	r.attachers = nil
	evs = append(evs, m.event(EventFailed, r))
	return append(evs, m.schedule()...)
}

func (m *model) register(at int64, in RequestInput) (uint64, []Event, error) {
	if in.URL == "" {
		return 0, nil, errInvalid("empty url")
	}
	if !in.Type.valid() {
		return 0, nil, errInvalid("unknown resource type")
	}
	if !in.Priority.valid() {
		return 0, nil, errInvalid("priority out of range")
	}
	if in.Type == TypePreload && (!in.As.valid() || in.As == TypePreload) {
		return 0, nil, errInvalid("preload must declare a non-preload resource type")
	}
	if in.Size < 0 {
		return 0, nil, errInvalid("negative size")
	}
	key := cacheKey{origin: in.Origin, url: in.URL}
	if in.Type == TypePreload {
		if _, dup := m.preloads[key]; dup {
			return 0, nil, errInvalid("duplicate preload registration")
		}
		if _, dup := m.cache[key]; dup {
			return 0, nil, errInvalid("duplicate preload registration")
		}
	}
	if at < m.now {
		return 0, nil, errRewind("timestamp before current clock")
	}
	m.now = at
	evs := m.expire()

	m.seq++
	m.nextID++
	as := in.Type
	if in.Type == TypePreload {
		as = in.As
	}
	r := &mReq{id: m.nextID, in: in, as: as, prio: in.Priority, seq: m.seq, state: StatePending}
	m.reqs[r.id] = r
	m.order = append(m.order, r.id)
	evs = append(evs, m.event(EventRegistered, r))

	if in.Type != TypePreload {
		if pid, ok := m.preloads[key]; ok {
			p := m.reqs[pid]
			if p.as == in.Type && p.in.Credentials == in.Credentials && p.in.Integrity == in.Integrity {
				r.state = StateAttached
				r.attachTo = p.id
				p.attachers = append(p.attachers, r.id)
				att := m.event(EventAttached, r)
				att.Target = p.id
				evs = append(evs, att)
				if in.Priority > p.prio {
					p.prio = in.Priority
					if p.state == StatePending {
						evs = append(evs, m.preempt(p)...)
					}
				}
				evs = append(evs, m.schedule()...)
				return r.id, evs, nil
			}
		}
		if e, ok := m.cache[key]; ok && e.matches(in.Type, in.Credentials, in.Integrity) {
			delete(m.cache, key)
			r.state = StateDone
			r.fromCache = true
			evs = append(evs, m.event(EventCacheHit, r), m.event(EventCompleted, r))
			return r.id, evs, nil
		}
	}

	if in.Type == TypePreload {
		m.preloads[key] = r.id
	}
	evs = append(evs, m.preempt(r)...)
	evs = append(evs, m.schedule()...)
	return r.id, evs, nil
}

func (m *model) lookup(at int64, id uint64) (*mReq, error) {
	if at < m.now {
		return nil, errRewind("timestamp before current clock")
	}
	r, ok := m.reqs[id]
	if !ok {
		return nil, errNotFound("unknown request id")
	}
	if r.state.Terminal() {
		return nil, errState("request already in a terminal state")
	}
	return r, nil
}

func (m *model) cancel(at int64, id uint64) ([]Event, error) {
	if id == 0 {
		return nil, errInvalid("zero request id")
	}
	r, err := m.lookup(at, id)
	if err != nil {
		return nil, err
	}
	m.now = at
	evs := m.expire()
	switch r.state {
	case StatePending:
		r.state = StateAborted
		if r.in.Type == TypePreload {
			delete(m.preloads, cacheKey{origin: r.in.Origin, url: r.in.URL})
		}
		evs = append(evs, m.event(EventAborted, r))
	case StateInFlight:
		r.state = StateAborted
		delete(m.inflight, r.id)
		if r.in.Type == TypePreload {
			delete(m.preloads, cacheKey{origin: r.in.Origin, url: r.in.URL})
		}
		for _, aid := range r.attachers {
			a := m.reqs[aid]
			a.state = StateFailed
			a.hasErr = true
			a.errKind = ErrAborted
			a.attachTo = 0
			evs = append(evs, m.event(EventFailed, a))
		}
		r.attachers = nil
		evs = append(evs, m.event(EventAborted, r))
		evs = append(evs, m.schedule()...)
	case StateAttached:
		p := m.reqs[r.attachTo]
		for i, aid := range p.attachers {
			if aid == r.id {
				p.attachers = append(p.attachers[:i], p.attachers[i+1:]...)
				break
			}
		}
		r.attachTo = 0
		r.state = StateAborted
		evs = append(evs, m.event(EventAborted, r))
	}
	return evs, nil
}

func (m *model) progress(at int64, id uint64, n int64) ([]Event, error) {
	if id == 0 {
		return nil, errInvalid("zero request id")
	}
	if n < 0 {
		return nil, errInvalid("negative progress")
	}
	r, err := m.lookup(at, id)
	if err != nil {
		return nil, err
	}
	if r.state != StateInFlight {
		return nil, errState("request is not in flight")
	}
	m.now = at
	evs := m.expire()
	r.progress += n
	if r.progress >= r.in.Size {
		evs = append(evs, m.complete(r)...)
	}
	return evs, nil
}

func (m *model) failOp(at int64, id uint64) ([]Event, error) {
	if id == 0 {
		return nil, errInvalid("zero request id")
	}
	r, err := m.lookup(at, id)
	if err != nil {
		return nil, err
	}
	if r.state != StateInFlight {
		return nil, errState("request is not in flight")
	}
	m.now = at
	evs := m.expire()
	return append(evs, m.fail(r)...), nil
}

func (m *model) advanceClock(at int64) ([]Event, error) {
	if at < m.now {
		return nil, errRewind("timestamp before current clock")
	}
	m.now = at
	return m.expire(), nil
}

func (m *model) dump() string {
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d inflight=%d\n", m.now, len(m.inflight))
	for _, id := range m.order {
		r := m.reqs[id]
		fmt.Fprintf(&b, "req %d st=%s prio=%s prog=%d pauses=%d fc=%v err=%v/%s att=%d\n",
			id, r.state, r.prio, r.progress, r.pauses, r.fromCache, r.hasErr, r.errKind, r.attachTo)
	}
	keys := make([]string, 0, len(m.cache))
	for k, e := range m.cache {
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%s|%s|%d", k.origin, k.url, e.as, e.cred, e.integrity, e.completedAt))
	}
	slices.Sort(keys)
	fmt.Fprintf(&b, "cache=%v\nwasted=%d\n", keys, len(m.wasted))
	return b.String()
}

func (s *Scheduler) dump() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d inflight=%d\n", s.now, s.inFlightTotal)
	ids := make([]uint64, 0, len(s.reqs))
	for id := range s.reqs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		r := s.reqs[id]
		var att uint64
		if r.attachTo != nil {
			att = r.attachTo.id
		}
		fmt.Fprintf(&b, "req %d st=%s prio=%s prog=%d pauses=%d fc=%v err=%v/%s att=%d\n",
			id, r.state, r.prio, r.progress, r.pauses, r.fromCache, r.hasErr, r.errKind, att)
	}
	keys := make([]string, 0, len(s.cache.entries))
	for k, e := range s.cache.entries {
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%s|%s|%d", k.origin, k.url, e.as, e.cred, e.integrity, e.completedAt))
	}
	slices.Sort(keys)
	fmt.Fprintf(&b, "cache=%v\nwasted=%d\n", keys, len(s.cache.wasted))
	return b.String()
}

func errKindOf(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		return e.Kind.String()
	}
	return "unknown"
}

func randInput(rng *rand.Rand) RequestInput {
	hosts := []string{"a", "b", "c"}
	urls := []string{"/u1", "/u2", "/u3", "/u4", "/u5", "/u6"}
	creds := []CredentialsMode{CredentialsOmit, CredentialsSameOrigin, CredentialsInclude}
	integrities := []string{"", "sha256-i1"}
	in := RequestInput{
		Origin:      org(hosts[rng.Intn(len(hosts))]),
		URL:         urls[rng.Intn(len(urls))],
		Type:        ResourceType(rng.Intn(int(typeCount))),
		Priority:    Priority(rng.Intn(int(PriorityHighest) + 1)),
		Credentials: creds[rng.Intn(len(creds))],
		Integrity:   integrities[rng.Intn(len(integrities))],
		Size:        int64(1 + rng.Intn(8)),
	}
	if in.Type == TypePreload {
		in.As = ResourceType(rng.Intn(int(TypePreload)))
	}
	switch rng.Intn(30) {
	case 0:
		in.URL = ""
	case 1:
		in.Type = ResourceType(99)
	case 2:
		in.Priority = Priority(9)
	}
	return in
}

func TestModelAgainstRandomOps(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 5, 8, 13, 21} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				PerOriginLimit: 1 + rng.Intn(3),
				GlobalLimit:    1 + rng.Intn(4),
				MaxPauses:      rng.Intn(3),
				PreloadTTL:     int64(rng.Intn(12)),
			}
			s, err := NewScheduler(cfg)
			if err != nil {
				t.Fatalf("NewScheduler: %v", err)
			}
			m := newModel(cfg)
			var got []Event
			s.Subscribe(func(e Event) { got = append(got, e) })
			t.Logf("cfg=%+v", cfg)

			var now int64
			nextAt := func() int64 {
				if rng.Intn(25) == 0 {
					return now - 1
				}
				now += int64(rng.Intn(3))
				return now
			}

			check := func(op string, want []Event, sErr, mErr error) {
				t.Helper()
				if errKindOf(sErr) != errKindOf(mErr) {
					t.Fatalf("op %s: scheduler err=%v, model err=%v", op, sErr, mErr)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("op %s: events differ\nscheduler=%v\nmodel=%v", op, got, want)
				}
				sd, md := s.dump(), m.dump()
				if sd != md {
					t.Fatalf("op %s: state differs\nscheduler:\n%s\nmodel:\n%s", op, sd, md)
				}
				t.Logf("op=%s out=%s events=%d why=scheduler matches model", op, errKindOf(sErr), len(got))
				got = got[:0]
			}

			for i := 0; i < 500; i++ {
				at := nextAt()
				id := uint64(1 + rng.Intn(int(m.nextID)+2))
				switch rng.Intn(100) {
				case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
					10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
					20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
					30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
					40, 41, 42, 43, 44:
					in := randInput(rng)
					sid, sErr := s.Register(at, in)
					mid, mEvs, mErr := m.register(at, in)
					if sErr == nil && mErr == nil && sid != mid {
						t.Fatalf("op %d register: id %d vs %d", i, sid, mid)
					}
					check(fmt.Sprintf("%d register %s/%s", i, in.Origin.Host, in.URL), mEvs, sErr, mErr)
				case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54,
					55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
					n := int64(rng.Intn(6))
					sErr := s.Progress(at, id, n)
					mEvs, mErr := m.progress(at, id, n)
					check(fmt.Sprintf("%d progress id=%d +%d", i, id, n), mEvs, sErr, mErr)
				case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74,
					75, 76, 77, 78, 79:
					sErr := s.Cancel(at, id)
					mEvs, mErr := m.cancel(at, id)
					check(fmt.Sprintf("%d cancel id=%d", i, id), mEvs, sErr, mErr)
				case 80, 81, 82, 83, 84:
					sErr := s.Fail(at, id)
					mEvs, mErr := m.failOp(at, id)
					check(fmt.Sprintf("%d fail id=%d", i, id), mEvs, sErr, mErr)
				default:
					sErr := s.AdvanceClock(at)
					mEvs, mErr := m.advanceClock(at)
					check(fmt.Sprintf("%d clock at=%d", i, at), mEvs, sErr, mErr)
				}
			}
		})
	}
}

func TestConcurrentOpsPreserveInvariants(t *testing.T) {
	cfg := Config{PerOriginLimit: 2, GlobalLimit: 4, MaxPauses: 2, PreloadTTL: 30}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	var violations atomic.Int32
	lastProgress := make(map[uint64]int64)
	s.check = func() {
		if s.inFlightTotal > cfg.GlobalLimit {
			violations.Add(1)
		}
		for o, n := range s.inFlightPerOrig {
			if n > cfg.PerOriginLimit {
				violations.Add(1)
				_ = o
			}
		}
		for id, r := range s.reqs {
			if r.progress < lastProgress[id] {
				violations.Add(1)
			}
			lastProgress[id] = r.progress
		}
	}

	var ts atomic.Int64
	var maxID atomic.Uint64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				at := ts.Add(1)
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4:
					in := randInput(rng)
					in.URL = fmt.Sprintf("/c%d", rng.Intn(40))
					if id, err := s.Register(at, in); err == nil {
						for {
							cur := maxID.Load()
							if id <= cur || maxID.CompareAndSwap(cur, id) {
								break
							}
						}
					}
				case 5, 6, 7:
					if id := maxID.Load(); id > 0 {
						_ = s.Progress(at, 1+uint64(rng.Intn(int(id))), int64(rng.Intn(4)))
					}
				case 8:
					if id := maxID.Load(); id > 0 {
						_ = s.Cancel(at, 1+uint64(rng.Intn(int(id))))
					}
				default:
					_ = s.AdvanceClock(at)
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()

	if violations.Load() != 0 {
		t.Fatalf("invariant violations: %d", violations.Load())
	}
	st := s.Stats()
	t.Logf("in=8 goroutines x 300 ops out=in-flight %d/%d pending=%d why=serialized under mutex",
		st.InFlightTotal, cfg.GlobalLimit, st.Pending)
	if st.InFlightTotal > cfg.GlobalLimit {
		t.Fatalf("final in-flight=%d exceeds global limit", st.InFlightTotal)
	}
}

func BenchmarkSelectNext(b *testing.B) {
	for _, n := range []int{100, 10000, 100000} {
		b.Run(fmt.Sprintf("pending=%d", n), func(b *testing.B) {
			s, err := NewScheduler(Config{PerOriginLimit: 1, GlobalLimit: 1, MaxPauses: 1, PreloadTTL: 10})
			if err != nil {
				b.Fatal(err)
			}
			hosts := []string{"a", "b", "c", "d"}
			for i := 0; i < n; i++ {
				in := reqIn(hosts[i%len(hosts)], fmt.Sprintf("/r%d", i), TypeScript, PriorityMedium)
				if _, err := s.Register(int64(i)+1, in); err != nil {
					b.Fatal(err)
				}
			}
			full := func(Origin) bool { return false }
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if s.pending.selectNext(full) == nil {
					b.Fatal("no selectable request")
				}
			}
		})
	}
}

func BenchmarkCacheHit(b *testing.B) {
	for _, n := range []int{10, 1000, 100000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			c := newPreloadCache(1 << 60)
			var target cacheKey
			for i := 0; i < n; i++ {
				k := cacheKey{origin: org("a"), url: fmt.Sprintf("/e%d", i)}
				c.put(k, &cacheEntry{as: TypeScript, cred: CredentialsInclude, completedAt: 1})
				if i == n/2 {
					target = k
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e := c.get(target)
				if e == nil || !e.matches(TypeScript, CredentialsInclude, "") {
					b.Fatal("hit failed")
				}
			}
		})
	}
}
