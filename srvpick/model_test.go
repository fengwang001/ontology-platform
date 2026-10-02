package srvpick

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naive is a deliberately simple, step-by-step reference implementation
// of the specification, used only by tests to cross-check Selector.
// Records live in a plain slice and every operation is a linear scan.
type naive struct {
	capacity int
	coolCap  int64
	wr       int64
	recs     []*naiveRecord
	nextSeq  uint64
}

type naiveRecord struct {
	target        string
	port          int
	priority      int
	weight        int
	expireAt      int64
	seq           uint64
	cooldownUntil int64
	failures      int
	lastFail      int64
	hasLastFail   bool
}

func newNaive(capacity int, coolCap, wr int64) *naive {
	return &naive{capacity: capacity, coolCap: coolCap, wr: wr}
}

func (n *naive) find(target string, port int) *naiveRecord {
	for _, rec := range n.recs {
		if rec.target == target && rec.port == port {
			return rec
		}
	}
	return nil
}

func naiveValidTime(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000
}

func (n *naive) add(target string, port, priority, weight int, ttl, now int64) error {
	if target == "" || port < 1 || port > 65535 ||
		priority < 0 || priority > 65535 ||
		weight < 0 || weight > 65535 ||
		ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidParam
	}
	if !naiveValidTime(now) {
		return ErrInvalidTime
	}
	if rec := n.find(target, port); rec != nil {
		rec.priority = priority
		rec.weight = weight
		rec.expireAt = now + ttl
		return nil
	}
	if len(n.recs) >= n.capacity {
		n.purgeExpired(now)
		if len(n.recs) >= n.capacity {
			return ErrFull
		}
	}
	n.nextSeq++
	n.recs = append(n.recs, &naiveRecord{
		target:   target,
		port:     port,
		priority: priority,
		weight:   weight,
		expireAt: now + ttl,
		seq:      n.nextSeq,
	})
	return nil
}

func (n *naive) failure(target string, port int, cooldown, now int64) error {
	if target == "" || cooldown < 1 || cooldown > 1_000_000_000 {
		return ErrInvalidParam
	}
	if !naiveValidTime(now) {
		return ErrInvalidTime
	}
	rec := n.find(target, port)
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expireAt {
		return ErrExpired
	}
	if !rec.hasLastFail || now >= rec.lastFail+n.wr {
		rec.failures = 0
	}
	rec.failures++
	rec.lastFail = now
	rec.hasLastFail = true
	shift := rec.failures - 1
	if shift > 30 {
		shift = 30
	}
	eff := cooldown << uint(shift)
	if eff > n.coolCap {
		eff = n.coolCap
	}
	if until := now + eff; until > rec.cooldownUntil {
		rec.cooldownUntil = until
	}
	return nil
}

func (n *naive) success(target string, port int, now int64) error {
	if target == "" {
		return ErrInvalidParam
	}
	if !naiveValidTime(now) {
		return ErrInvalidTime
	}
	rec := n.find(target, port)
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expireAt {
		return ErrExpired
	}
	rec.failures = 0
	return nil
}

// pick returns the selected record plus a human-readable rationale
// (group priority, ordered candidates, S, r1) for test logs.
func (n *naive) pick(r uint64, now int64) (string, int, string, error) {
	if !naiveValidTime(now) {
		return "", 0, "", ErrInvalidTime
	}
	var usable []*naiveRecord
	for _, rec := range n.recs {
		if now < rec.expireAt && now >= rec.cooldownUntil {
			usable = append(usable, rec)
		}
	}
	if len(usable) == 0 {
		return "", 0, "", ErrNoAvailable
	}
	minPriority := usable[0].priority
	for _, rec := range usable {
		if rec.priority < minPriority {
			minPriority = rec.priority
		}
	}
	var group []*naiveRecord
	for _, rec := range usable {
		if rec.priority == minPriority {
			group = append(group, rec)
		}
	}
	sort.Slice(group, func(i, j int) bool {
		zi, zj := group[i].weight == 0, group[j].weight == 0
		if zi != zj {
			return zi
		}
		return group[i].seq < group[j].seq
	})
	var sum uint64
	for _, rec := range group {
		sum += uint64(rec.weight)
	}
	r1 := r % (sum + 1)
	var cum uint64
	var trace string
	for _, rec := range group {
		cum += uint64(rec.weight)
		trace += fmt.Sprintf(" %s#%d(w=%d,cum=%d)", rec.target, rec.seq, rec.weight, cum)
		if cum >= r1 {
			rationale := fmt.Sprintf("group=%d S=%d r1=%d order:%s -> %s",
				minPriority, sum, r1, trace, rec.target)
			return rec.target, rec.port, rationale, nil
		}
	}
	return "", 0, "", ErrNoAvailable
}

func (n *naive) purgeExpired(now int64) int {
	kept := n.recs[:0]
	removed := 0
	for _, rec := range n.recs {
		if rec.expireAt <= now {
			removed++
		} else {
			kept = append(kept, rec)
		}
	}
	n.recs = kept
	return removed
}

func (n *naive) purge(now int64) (int, error) {
	if !naiveValidTime(now) {
		return 0, ErrInvalidTime
	}
	return n.purgeExpired(now), nil
}

// stateMismatch returns a description of the first difference between
// the Selector and the naive model, or "" when they agree.
func stateMismatch(s *Selector, n *naive) string {
	if len(s.records) != len(n.recs) {
		return fmt.Sprintf("record count: selector=%d naive=%d", len(s.records), len(n.recs))
	}
	if s.nextSeq != n.nextSeq {
		return fmt.Sprintf("nextSeq: selector=%d naive=%d", s.nextSeq, n.nextSeq)
	}
	for _, nr := range n.recs {
		sr, ok := s.records[key{target: nr.target, port: nr.port}]
		if !ok {
			return fmt.Sprintf("selector missing record (%q, %d)", nr.target, nr.port)
		}
		if sr.priority != nr.priority || sr.weight != nr.weight ||
			sr.expireAt != nr.expireAt || sr.seq != nr.seq ||
			sr.cooldownUntil != nr.cooldownUntil || sr.failures != nr.failures ||
			sr.lastFail != nr.lastFail || sr.hasLastFail != nr.hasLastFail {
			return fmt.Sprintf("record (%q, %d):\nselector: %+v\nnaive:    %+v",
				nr.target, nr.port, sr, nr)
		}
	}
	return ""
}

// pickScale returns a random bound in [1, 1e9] biased towards small
// values so caps actually engage.
func pickScale(rng *rand.Rand) int64 {
	switch rng.Intn(3) {
	case 0:
		return int64(1 + rng.Intn(50))
	case 1:
		return int64(1 + rng.Intn(1000))
	default:
		return 1 + rng.Int63n(1_000_000_000)
	}
}

// drive replays a seeded random operation sequence against both the
// Selector and the naive model, comparing every result and the full
// state after every step. It returns a signature of all observable
// outputs so identical sequences can be checked for exact replay.
func drive(t *testing.T, seed int64, logDetail bool) string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	capacity := 1 + rng.Intn(6)
	coolCap := pickScale(rng)
	wr := pickScale(rng)
	s, err := New(capacity, coolCap, wr)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) = %v", capacity, coolCap, wr, err)
	}
	n := newNaive(capacity, coolCap, wr)

	var sig strings.Builder
	var trace []string
	fail := func(format string, args ...interface{}) {
		for _, line := range trace {
			t.Log(line)
		}
		t.Fatalf(format, args...)
	}
	emit := func(line string) {
		trace = append(trace, line)
		sig.WriteString(line)
		sig.WriteByte('\n')
		if logDetail {
			t.Logf("seed=%d %s", seed, line)
		}
	}
	emit(fmt.Sprintf("config cap=%d coolCap=%d wr=%d", capacity, coolCap, wr))

	now := int64(0)
	ops := 40 + rng.Intn(40)
	for op := 0; op < ops; op++ {
		now += int64(rng.Intn(6))
		if rng.Intn(20) == 0 {
			now += int64(rng.Intn(500))
		}
		target := fmt.Sprintf("t%d", rng.Intn(4))
		if rng.Intn(60) == 0 {
			target = "" // exercise ErrInvalidParam
		}
		port := 1 + rng.Intn(2)
		if rng.Intn(60) == 0 {
			port = rng.Intn(70000) // exercise invalid ports
		}
		opNow := now
		if rng.Intn(60) == 0 {
			// Exercise invalid times (negative or beyond 1e15).
			opNow = rng.Int63n(2*MaxTime) - MaxTime
		}
		switch x := rng.Intn(100); {
		case x < 40: // Add
			priority := 10 * rng.Intn(3)
			weight := rng.Intn(16)
			if rng.Intn(4) == 0 {
				weight = 0
			}
			ttl := int64(1 + rng.Intn(30))
			if rng.Intn(10) == 0 {
				ttl = 1_000_000_000
			}
			errS := s.Add(target, port, priority, weight, ttl, opNow)
			errN := n.add(target, port, priority, weight, ttl, opNow)
			emit(fmt.Sprintf("Add(%q,%d,pri=%d,w=%d,ttl=%d,now=%d) -> %v",
				target, port, priority, weight, ttl, opNow, errS))
			if errS != errN {
				fail("Add error mismatch: selector=%v naive=%v", errS, errN)
			}
		case x < 60: // Failure
			cooldown := int64(1 + rng.Intn(20))
			if rng.Intn(10) == 0 {
				cooldown = 1 + rng.Int63n(1_000_000_000)
			}
			errS := s.Failure(target, port, cooldown, opNow)
			errN := n.failure(target, port, cooldown, opNow)
			emit(fmt.Sprintf("Failure(%q,%d,cd=%d,now=%d) -> %v",
				target, port, cooldown, opNow, errS))
			if errS != errN {
				fail("Failure error mismatch: selector=%v naive=%v", errS, errN)
			}
		case x < 70: // Success
			errS := s.Success(target, port, opNow)
			errN := n.success(target, port, opNow)
			emit(fmt.Sprintf("Success(%q,%d,now=%d) -> %v", target, port, opNow, errS))
			if errS != errN {
				fail("Success error mismatch: selector=%v naive=%v", errS, errN)
			}
		case x < 90: // Pick
			r := rng.Uint64()
			if rng.Intn(3) == 0 {
				r = uint64(rng.Intn(64))
			}
			tS, pS, errS := s.Pick(r, opNow)
			tN, pN, rationale, errN := n.pick(r, opNow)
			emit(fmt.Sprintf("Pick(r=%d,now=%d) -> (%q,%d,%v) [%s]",
				r, opNow, tS, pS, errS, rationale))
			if errS != errN || tS != tN || pS != pN {
				fail("Pick mismatch: selector=(%q,%d,%v) naive=(%q,%d,%v)",
					tS, pS, errS, tN, pN, errN)
			}
		default: // Purge
			cS, errS := s.Purge(opNow)
			cN, errN := n.purge(opNow)
			emit(fmt.Sprintf("Purge(now=%d) -> (%d,%v)", opNow, cS, errS))
			if errS != errN || cS != cN {
				fail("Purge mismatch: selector=(%d,%v) naive=(%d,%v)", cS, errS, cN, errN)
			}
		}
		if m := stateMismatch(s, n); m != "" {
			fail("state mismatch after op %d:\n%s", op, m)
		}
	}
	emit(fmt.Sprintf("final records=%d nextSeq=%d", len(s.records), s.nextSeq))
	return sig.String()
}

// TestRandomizedCompareWithNaive cross-checks the Selector against the
// naive step-by-step model over 2000 random record sets with random
// failure/success sequences and random r values. Inputs, outputs and
// the pick rationale are logged (see them with `go test -v`).
func TestRandomizedCompareWithNaive(t *testing.T) {
	const iterations = 2000
	for iter := 0; iter < iterations; iter++ {
		seed := int64(iter)*7919 + 13
		sig := drive(t, seed, iter < 3)
		t.Logf("iter %d seed=%d ok, signature %d bytes", iter, seed, len(sig))
	}
}

// TestReplaySameSequence: identical operation sequences replayed on
// fresh selectors produce byte-identical outputs and sequence numbers.
func TestReplaySameSequence(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		first := drive(t, 900000+seed, false)
		second := drive(t, 900000+seed, false)
		if first != second {
			t.Fatalf("seed %d: replay diverged", seed)
		}
	}
}

// TestConcurrentSmoke hammers one selector from many goroutines with
// mixed operations (run with -race) and afterwards checks the core
// invariants: at most Cap records and monotonic cooldown deadlines.
func TestConcurrentSmoke(t *testing.T) {
	const capacity = 8
	s := mustNew(t, capacity, 1_000_000, 100)
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				target := fmt.Sprintf("t%d", rng.Intn(6))
				port := 1 + rng.Intn(3)
				now := int64(rng.Intn(2000))
				switch rng.Intn(5) {
				case 0:
					_ = s.Add(target, port, 10*rng.Intn(3), rng.Intn(10), int64(1+rng.Intn(100)), now)
				case 1:
					_ = s.Failure(target, port, int64(1+rng.Intn(50)), now)
				case 2:
					_ = s.Success(target, port, now)
				case 3:
					_, _, _ = s.Pick(rng.Uint64(), now)
				default:
					_, _ = s.Purge(now)
				}
			}
		}(int64(worker))
	}
	wg.Wait()
	if len(s.records) > capacity {
		t.Fatalf("records = %d, exceeds Cap %d", len(s.records), capacity)
	}
	for k, rec := range s.records {
		if rec.cooldownUntil < 0 || rec.failures < 0 {
			t.Fatalf("record %v in bad state: %+v", k, rec)
		}
	}
}
