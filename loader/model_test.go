package loader

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive is an independently written, deliberately simple reference model:
// linear scans over all requests instead of heaps and tiered deques.
// It must agree with Scheduler on every operation.
type naive struct {
	cfg            Config
	now            int64
	seq            uint64
	startSeq       uint64
	nextID         uint64
	reqs           map[uint64]*request
	activePreloads map[cacheKey]*request
	cache          map[cacheKey]*cacheEntry
	waste          []WasteEntry
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:            cfg,
		reqs:           make(map[uint64]*request),
		activePreloads: make(map[cacheKey]*request),
		cache:          make(map[cacheKey]*cacheEntry),
	}
}

func (n *naive) counts() (map[Origin]int, int) {
	perOrigin := make(map[Origin]int)
	total := 0
	for _, r := range n.reqs {
		if r.state == StateTransferring {
			perOrigin[r.in.Origin]++
			total++
		}
	}
	return perOrigin, total
}

func (n *naive) sweep() {
	var expired []cacheKey
	for k, e := range n.cache {
		if n.now-e.completedAt > n.cfg.PreloadTTL {
			expired = append(expired, k)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		a, b := n.cache[expired[i]], n.cache[expired[j]]
		if a.completedAt != b.completedAt {
			return a.completedAt < b.completedAt
		}
		if expired[i].url != expired[j].url {
			return expired[i].url < expired[j].url
		}
		ai, bi := expired[i].origin, expired[j].origin
		if ai.Scheme != bi.Scheme {
			return ai.Scheme < bi.Scheme
		}
		if ai.Host != bi.Host {
			return ai.Host < bi.Host
		}
		return ai.Port < bi.Port
	})
	for _, k := range expired {
		e := n.cache[k]
		n.waste = append(n.waste, WasteEntry{Origin: k.origin, URL: k.url, CompletedAt: e.completedAt, WastedAt: n.now})
		delete(n.cache, k)
	}
}

func (n *naive) setClock(at int64) {
	n.now = at
	n.sweep()
}

// better reports whether a should be scheduled before b: higher effective
// priority first, then earlier registration.
func better(a, b *request) bool {
	if a.effPriority() != b.effPriority() {
		return a.effPriority() > b.effPriority()
	}
	return a.seq < b.seq
}

func (n *naive) schedule() {
	for {
		perOrigin, total := n.counts()
		var best *request
		for _, r := range n.reqs {
			if r.state != StatePending || perOrigin[r.in.Origin] >= n.cfg.PerOriginLimit {
				continue
			}
			if best == nil || better(r, best) {
				best = r
			}
		}
		if best != nil && total < n.cfg.GlobalLimit {
			best.state = StateTransferring
			n.startSeq++
			best.startSeq = n.startSeq
			continue
		}
		var top *request
		for _, r := range n.reqs {
			if r.state != StatePending || r.effPriority() != PriorityHighest {
				continue
			}
			if top == nil || r.seq < top.seq {
				top = r
			}
		}
		if top != nil && (perOrigin[top.in.Origin] >= n.cfg.PerOriginLimit || total >= n.cfg.GlobalLimit) {
			var victim *request
			for _, r := range n.reqs {
				if r.state != StateTransferring || r.in.Origin != top.in.Origin {
					continue
				}
				ep := r.effPriority()
				if ep >= PriorityHighest || r.pauses >= n.cfg.MaxPauses {
					continue
				}
				if victim == nil || ep < victim.effPriority() ||
					(ep == victim.effPriority() && r.startSeq > victim.startSeq) {
					victim = r
				}
			}
			if victim != nil {
				victim.state = StatePending
				victim.pauses++
				continue
			}
		}
		return
	}
}

func (n *naive) register(at int64, in RequestInput) (uint64, error) {
	if in.URL == "" {
		return 0, fmt.Errorf("%w: empty URL", ErrInvalidArgument)
	}
	if !in.Type.valid() {
		return 0, fmt.Errorf("%w: unknown resource type %d", ErrInvalidArgument, int(in.Type))
	}
	if !in.Priority.valid() {
		return 0, fmt.Errorf("%w: priority out of range: %d", ErrInvalidArgument, int(in.Priority))
	}
	if in.Size <= 0 {
		return 0, fmt.Errorf("%w: size must be positive", ErrInvalidArgument)
	}
	key := cacheKey{in.Origin, in.URL}
	if in.Type == TypePreload {
		if !in.As.valid() || in.As == TypePreload {
			return 0, fmt.Errorf("%w: preload destination type invalid", ErrInvalidArgument)
		}
		if _, ok := n.activePreloads[key]; ok {
			return 0, fmt.Errorf("%w: duplicate preload registration for %q", ErrInvalidArgument, in.URL)
		}
		if _, ok := n.cache[key]; ok {
			return 0, fmt.Errorf("%w: duplicate preload registration for %q", ErrInvalidArgument, in.URL)
		}
	}
	if at < n.now {
		return 0, fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, n.now)
	}

	n.setClock(at)
	n.seq++
	n.nextID++
	r := &request{id: n.nextID, seq: n.seq, in: in, state: StatePending}
	n.reqs[r.id] = r

	if in.Type == TypePreload {
		n.activePreloads[key] = r
	} else if e, ok := n.cache[key]; ok && e.matches(in) {
		delete(n.cache, key)
		r.state = StateCompleted
		r.progress = in.Size
		r.fromCache = true
		return r.id, nil
	} else if p, ok := n.activePreloads[key]; ok && p.startSeq > 0 {
		r.state = StateAttached
		r.attachedTo = p
		p.attachers = append(p.attachers, r)
		n.schedule()
		return r.id, nil
	}
	n.schedule()
	return r.id, nil
}

func (n *naive) advance(id uint64, delta int64, at int64) error {
	if id == 0 || delta <= 0 {
		return fmt.Errorf("%w: id and positive delta required", ErrInvalidArgument)
	}
	if at < n.now {
		return fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, n.now)
	}
	r, ok := n.reqs[id]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	if r.state != StateTransferring {
		return fmt.Errorf("%w: advance in state %s", ErrInvalidState, r.state)
	}
	n.setClock(at)
	r.progress += delta
	if r.progress >= r.in.Size {
		n.terminate(r, StateCompleted, nil)
		n.schedule()
	}
	return nil
}

func (n *naive) stop(id uint64, at int64, state State, cause error) error {
	if id == 0 {
		return fmt.Errorf("%w: id required", ErrInvalidArgument)
	}
	if at < n.now {
		return fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, n.now)
	}
	r, ok := n.reqs[id]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	if r.state.terminal() {
		return fmt.Errorf("%w: already %s", ErrInvalidState, r.state)
	}
	n.setClock(at)
	n.terminate(r, state, cause)
	n.schedule()
	return nil
}

func (n *naive) tick(delta int64) error {
	if delta < 0 {
		return fmt.Errorf("%w: delta=%d", ErrClockSkew, delta)
	}
	n.setClock(n.now + delta)
	return nil
}

func (n *naive) terminate(r *request, state State, cause error) {
	if r.attachedTo != nil {
		p := r.attachedTo
		for i, a := range p.attachers {
			if a == r {
				p.attachers = append(p.attachers[:i], p.attachers[i+1:]...)
				break
			}
		}
		r.attachedTo = nil
	}
	r.state = state
	r.cause = cause
	if r.in.Type == TypePreload {
		key := cacheKey{r.in.Origin, r.in.URL}
		delete(n.activePreloads, key)
		if state == StateCompleted {
			if _, ok := n.cache[key]; !ok {
				n.cache[key] = &cacheEntry{
					as:          r.in.As,
					credentials: r.in.Credentials,
					integrity:   r.in.Integrity,
					completedAt: n.now,
				}
			}
		}
	}
	for _, a := range r.attachers {
		a.attachedTo = nil
		a.state = state
		if state == StateCompleted {
			a.progress = a.in.Size
		}
		if state == StateAborted {
			a.cause = ErrAborted
		}
	}
	r.attachers = nil
}

func (n *naive) status(id uint64) (Status, error) {
	r, ok := n.reqs[id]
	if !ok {
		return Status{}, fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	st := Status{
		State:             r.state,
		Progress:          r.progress,
		Size:              r.in.Size,
		Pauses:            r.pauses,
		EffectivePriority: r.effPriority(),
		FromCache:         r.fromCache,
		Cause:             r.cause,
	}
	if r.attachedTo != nil {
		st.AttachedTo = r.attachedTo.id
	}
	return st, nil
}

func kindOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, ErrClockSkew):
		return "clock-skew"
	case errors.Is(err, ErrNotFound):
		return "not-found"
	case errors.Is(err, ErrInvalidState):
		return "invalid-state"
	}
	return "unknown"
}

// TestDifferentialRandom drives the scheduler and the naive model with
// identical random operation sequences and compares all observable state
// after every step, logging inputs, outputs and the deciding rule.
func TestDifferentialRandom(t *testing.T) {
	origins := []Origin{originA, originB, originC}
	creds := []string{"", "omit", "include"}
	integrities := []string{"", "h1", "h2"}
	cfg := Config{PerOriginLimit: 2, GlobalLimit: 3, MaxPauses: 2, PreloadTTL: 5}

	for _, seed := range []int64{7, 42, 2024} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s, err := NewScheduler(cfg)
			if err != nil {
				t.Fatal(err)
			}
			n := newNaive(cfg)
			lastProgress := make(map[uint64]int64)

			randomInput := func() RequestInput {
				in := RequestInput{
					Origin:      origins[rng.Intn(len(origins))],
					URL:         fmt.Sprintf("/u%d", rng.Intn(6)),
					Priority:    Priority(rng.Intn(numPriorities)),
					Credentials: creds[rng.Intn(len(creds))],
					Integrity:   integrities[rng.Intn(len(integrities))],
					Size:        1 + rng.Int63n(8),
				}
				if rng.Intn(100) < 20 {
					in.Type = TypePreload
					in.As = ResourceType(rng.Intn(int(TypePreload)))
				} else {
					in.Type = ResourceType(rng.Intn(int(TypePreload)))
				}
				if rng.Intn(100) < 15 {
					in.Priority = PriorityHighest
				}
				return in
			}

			for op := 0; op < 3000; op++ {
				now := s.Now()
				at := now + rng.Int63n(3)
				if rng.Intn(100) < 4 {
					at = now - 1 - rng.Int63n(5) // provoke clock skew
				}
				var desc string
				var sErr, nErr error

				switch rng.Intn(100) {
				case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
					20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34: // 35% register
					in := randomInput()
					var sID, nID uint64
					sID, sErr = s.Register(at, in)
					nID, nErr = n.register(at, in)
					desc = fmt.Sprintf("register(%s %s %s prio=%s cred=%q sri=%q size=%d at=%d)",
						in.Origin.Host, in.URL, in.Type, in.Priority, in.Credentials, in.Integrity, in.Size, at)
					if kindOf(sErr) == kindOf(nErr) && sErr == nil && sID != nID {
						t.Fatalf("op %d: id mismatch sched=%d naive=%d", op, sID, nID)
					}
					desc += fmt.Sprintf(" -> id=%d/%d", sID, nID)
				case 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49,
					50, 51, 52, 53, 54, 55, 56, 57, 58, 59: // 25% advance
					id := uint64(1 + rng.Int63n(int64(s.nextID)+1))
					delta := int64(1 + rng.Intn(3))
					sErr = s.Advance(id, delta, at)
					nErr = n.advance(id, delta, at)
					desc = fmt.Sprintf("advance(id=%d delta=%d at=%d)", id, delta, at)
				case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71: // 12% abort
					id := uint64(1 + rng.Int63n(int64(s.nextID)+1))
					sErr = s.Abort(id, at)
					nErr = n.stop(id, at, StateAborted, ErrAborted)
					desc = fmt.Sprintf("abort(id=%d at=%d)", id, at)
				case 72, 73, 74, 75, 76, 77: // 6% fail
					id := uint64(1 + rng.Int63n(int64(s.nextID)+1))
					sErr = s.Fail(id, at)
					nErr = n.stop(id, at, StateFailed, nil)
					desc = fmt.Sprintf("fail(id=%d at=%d)", id, at)
				default: // ~22% tick
					delta := rng.Int63n(4)
					if rng.Intn(100) < 10 {
						delta = -delta
					}
					sErr = s.Tick(delta)
					nErr = n.tick(delta)
					desc = fmt.Sprintf("tick(delta=%d)", delta)
				}

				if kindOf(sErr) != kindOf(nErr) {
					t.Fatalf("op %d %s: sched err=%v naive err=%v", op, desc, sErr, nErr)
				}
				t.Logf("op=%d in=%s out=%s why=%s", op, desc, kindOf(sErr), verdictOf(desc, sErr))

				// Compare all observable state after every operation.
				for id := uint64(1); id <= s.nextID; id++ {
					ss, err1 := s.Status(id)
					ns, err2 := n.status(id)
					if (err1 == nil) != (err2 == nil) {
						t.Fatalf("op %d: id=%d existence mismatch", op, id)
					}
					if err1 != nil {
						continue
					}
					if ss.State != ns.State || ss.Progress != ns.Progress || ss.Pauses != ns.Pauses ||
						ss.EffectivePriority != ns.EffectivePriority || ss.AttachedTo != ns.AttachedTo ||
						ss.FromCache != ns.FromCache || kindOfCause(ss.Cause) != kindOfCause(ns.Cause) {
						t.Fatalf("op %d %s: id=%d sched=%+v naive=%+v", op, desc, id, ss, ns)
					}
					if ss.Progress < lastProgress[id] {
						t.Fatalf("op %d: id=%d progress regressed %d -> %d", op, id, lastProgress[id], ss.Progress)
					}
					lastProgress[id] = ss.Progress
				}
				if !reflect.DeepEqual(s.Waste(), n.waste) {
					t.Fatalf("op %d %s: waste sched=%v naive=%v", op, desc, s.Waste(), n.waste)
				}
				if s.CacheSize() != len(n.cache) {
					t.Fatalf("op %d %s: cache size sched=%d naive=%d", op, desc, s.CacheSize(), len(n.cache))
				}
				perOrigin, total := s.Stats()
				for o, c := range perOrigin {
					if c > cfg.PerOriginLimit {
						t.Fatalf("op %d: origin %v in-flight %d > per-origin limit", op, o, c)
					}
				}
				if total > cfg.GlobalLimit {
					t.Fatalf("op %d: total in-flight %d > global limit", op, total)
				}
			}
			t.Logf("verdict: 3000 random ops, scheduler and naive model agree on all state")
		})
	}
}

func kindOfCause(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrAborted) {
		return "aborted"
	}
	return "other"
}

func verdictOf(desc string, err error) string {
	if err != nil {
		return "rejected before any mutation"
	}
	return "applied; quotas/cache/clock updated per rule"
}
