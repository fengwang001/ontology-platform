package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// logEvent 打印一次跟踪事件：操作、是否驱逐、判定依据与事后各键状态。
func logEvent(t *testing.T, s *BoundedSet, key string, ts int64, err error) {
	t.Helper()
	if err != nil {
		t.Logf("event track(%q, %d) -> rejected: %v", key, ts, err)
		logStates(t, s)
		return
	}
	snap := s.Snapshot()
	t.Logf("event track(%q, %d) -> ok, size=%d evictions=%d", key, ts, s.Count(), s.Evictions())
	logStates(t, s)
	_ = snap
}

func logStates(t *testing.T, s *BoundedSet) {
	t.Helper()
	for _, st := range s.Snapshot() {
		t.Logf("  state key=%q ts=%d slot=%d", st.Key, st.Timestamp, st.Slot)
	}
}

// logEviction 在预期发生驱逐时打印受害者与判定依据。
func logEviction(t *testing.T, before []KeyState, victim KeyState) {
	t.Helper()
	t.Logf("  evict key=%q ts=%d slot=%d | reason: min (timestamp, slot) among %d members",
		victim.Key, victim.Timestamp, victim.Slot, len(before))
	for _, st := range before {
		mark := " "
		if st.Key == victim.Key {
			mark = "*"
		}
		t.Logf("  %s candidate key=%q ts=%d slot=%d", mark, st.Key, st.Timestamp, st.Slot)
	}
}

// expectVictim 按驱逐序 (ts, slot) 求预期受害者。
func expectVictim(snap []KeyState) KeyState {
	v := snap[0]
	for _, st := range snap[1:] {
		if st.Timestamp < v.Timestamp || (st.Timestamp == v.Timestamp && st.Slot < v.Slot) {
			v = st
		}
	}
	return v
}

func mustNew(t *testing.T, capacity int) *BoundedSet {
	t.Helper()
	s, err := NewBoundedSet(capacity)
	if err != nil {
		t.Fatalf("NewBoundedSet(%d): %v", capacity, err)
	}
	return s
}

func keysOf(snap []KeyState) []string {
	out := make([]string, 0, len(snap))
	for _, st := range snap {
		out = append(out, st.Key)
	}
	return out
}

func sameMembers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func TestEvictionOnTimestampTie(t *testing.T) {
	s := mustNew(t, 2)

	if err := s.Track("a", 10); err != nil {
		t.Fatal(err)
	}
	logEvent(t, s, "a", 10, nil)
	if err := s.Track("b", 10); err != nil {
		t.Fatal(err)
	}
	logEvent(t, s, "b", 10, nil)

	before := s.Snapshot()
	victim := expectVictim(before)
	if victim.Key != "a" {
		t.Fatalf("expected victim a (tie ts=10, smaller slot), got %q", victim.Key)
	}
	if err := s.Track("c", 20); err != nil {
		t.Fatal(err)
	}
	logEvent(t, s, "c", 20, nil)
	logEviction(t, before, victim)

	if got := keysOf(s.Snapshot()); !sameMembers(got, []string{"b", "c"}) {
		t.Fatalf("members = %v, want [b c]", got)
	}
	if s.Count() != 2 {
		t.Fatalf("count = %d, want 2", s.Count())
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestTimestampRegression(t *testing.T) {
	s := mustNew(t, 2)

	_ = s.Track("a", 100)
	logEvent(t, s, "a", 100, nil)
	_ = s.Track("b", 50)
	logEvent(t, s, "b", 50, nil)

	// 回退：把 a 的时间戳刷新为更小的 1，a 变为最旧。
	if err := s.Track("a", 1); err != nil {
		t.Fatal(err)
	}
	logEvent(t, s, "a", 1, nil)
	if s.Count() != 2 {
		t.Fatalf("count = %d, want 2 (refresh must not grow set)", s.Count())
	}

	before := s.Snapshot()
	victim := expectVictim(before)
	if victim.Key != "a" {
		t.Fatalf("expected victim a after regression, got %q", victim.Key)
	}
	_ = s.Track("c", 60)
	logEvent(t, s, "c", 60, nil)
	logEviction(t, before, victim)

	if got := keysOf(s.Snapshot()); !sameMembers(got, []string{"b", "c"}) {
		t.Fatalf("members = %v, want [b c]", got)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestEvictedKeyReentryGetsFreshSlot(t *testing.T) {
	s := mustNew(t, 2)

	_ = s.Track("a", 1)
	_ = s.Track("b", 2)
	logEvent(t, s, "b", 2, nil)

	before := s.Snapshot()
	_ = s.Track("c", 3) // 驱逐 a（ts 最小）
	logEvent(t, s, "c", 3, nil)
	logEviction(t, before, expectVictim(before))

	// a 重新进入：按新键处理，分配不复用的新位点。
	if err := s.Track("a", 4); err != nil {
		t.Fatal(err)
	}
	logEvent(t, s, "a", 4, nil)

	var aState KeyState
	found := false
	for _, st := range s.Snapshot() {
		if st.Key == "a" {
			aState = st
			found = true
		}
	}
	if !found {
		t.Fatal("a should be back in the set")
	}
	if aState.Slot != 3 {
		t.Fatalf("a slot = %d, want 3 (fresh, non-reused slot)", aState.Slot)
	}
	if got := keysOf(s.Snapshot()); !sameMembers(got, []string{"c", "a"}) {
		t.Fatalf("members = %v, want [c a]", got)
	}
	if s.Evictions() != 2 {
		t.Fatalf("evictions = %d, want 2", s.Evictions())
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestInvalidInputsRejectedAtomically(t *testing.T) {
	if _, err := NewBoundedSet(0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("capacity 0: err = %v, want ErrInvalidCapacity", err)
	}
	if _, err := NewBoundedSet(-3); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("capacity -3: err = %v, want ErrInvalidCapacity", err)
	}

	s := mustNew(t, 2)
	_ = s.Track("a", 5)
	_ = s.Track("b", 6)
	before := s.Snapshot()
	beforeEvictions := s.Evictions()

	if err := s.Track("", 7); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key: err = %v, want ErrEmptyKey", err)
	}
	logEvent(t, s, "", 7, ErrEmptyKey)
	if err := s.Track("c", -1); !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("negative ts: err = %v, want ErrNegativeTimestamp", err)
	}
	logEvent(t, s, "c", -1, ErrNegativeTimestamp)
	if errors.Is(ErrEmptyKey, ErrNegativeTimestamp) || errors.Is(ErrNegativeTimestamp, ErrInvalidCapacity) {
		t.Fatal("failure reasons must be distinguishable")
	}

	after := s.Snapshot()
	if s.Count() != 2 || s.Evictions() != beforeEvictions {
		t.Fatalf("failed calls mutated state: count=%d evictions=%d", s.Count(), s.Evictions())
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("snapshot changed after rejected calls: %v -> %v", before, after)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

func TestConcurrentTrackDistinctKeys(t *testing.T) {
	const (
		workers  = 64
		capacity = 100
	)
	s := mustNew(t, capacity)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 并发读：不得读到中间态（大小永不超过容量，快照长度等于计数）。
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				if len(snap) > capacity {
					t.Errorf("snapshot size %d exceeds capacity", len(snap))
					return
				}
				if err := s.SelfCheck(); err != nil {
					t.Errorf("selfcheck: %v", err)
					return
				}
			}
		}()
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%03d", id)
			if err := s.Track(key, int64(id)); err != nil {
				t.Errorf("track %q: %v", key, err)
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	if s.Count() != workers {
		t.Fatalf("count = %d, want %d", s.Count(), workers)
	}
	snap := s.Snapshot()
	if len(snap) != workers {
		t.Fatalf("snapshot len = %d, want %d", len(snap), workers)
	}
	seen := map[string]bool{}
	for _, st := range snap {
		if seen[st.Key] {
			t.Fatalf("duplicate key %q in snapshot", st.Key)
		}
		seen[st.Key] = true
		if st.Timestamp < 0 || st.Timestamp >= workers {
			t.Fatalf("key %q has unexpected ts %d", st.Key, st.Timestamp)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
	t.Logf("concurrent: %d distinct keys tracked, count=%d snapshot=%d", workers, s.Count(), len(snap))
}

// naiveModel 是用朴素映射实现的对照模型，逻辑独立于 BoundedSet。
type naiveModel struct {
	capacity int
	entries  map[string]KeyState
	nextSlot uint64
}

func newNaiveModel(capacity int) *naiveModel {
	return &naiveModel{capacity: capacity, entries: map[string]KeyState{}}
}

func (m *naiveModel) track(key string, ts int64) {
	if st, ok := m.entries[key]; ok {
		st.Timestamp = ts
		m.entries[key] = st
		return
	}
	m.entries[key] = KeyState{Key: key, Timestamp: ts, Slot: m.nextSlot}
	m.nextSlot++
	if len(m.entries) > m.capacity {
		var victim KeyState
		first := true
		for _, st := range m.entries {
			if first || st.Timestamp < victim.Timestamp ||
				(st.Timestamp == victim.Timestamp && st.Slot < victim.Slot) {
				victim = st
				first = false
			}
		}
		delete(m.entries, victim.Key)
	}
}

func (m *naiveModel) snapshot() []KeyState {
	out := make([]KeyState, 0, len(m.entries))
	for _, st := range m.entries {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp < out[j].Timestamp
		}
		return out[i].Slot < out[j].Slot
	})
	return out
}

func TestAgainstNaiveModel(t *testing.T) {
	const (
		capacity = 7
		keyspace = 12 // 大于容量，保证频繁驱逐与重进
		steps    = 2000
	)
	s := mustNew(t, capacity)
	model := newNaiveModel(capacity)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < steps; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(keyspace))
		ts := int64(rng.Intn(20)) // 小范围时间戳，制造大量并列与回退
		if err := s.Track(key, ts); err != nil {
			t.Fatalf("step %d track(%q, %d): %v", i, key, ts, err)
		}
		model.track(key, ts)

		got := s.Snapshot()
		want := model.snapshot()
		if s.Count() != len(want) {
			t.Fatalf("step %d: count = %d, want %d", i, s.Count(), len(want))
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d: snapshot mismatch\ngot  %v\nwant %v", i, got, want)
		}
		if i%500 == 0 {
			t.Logf("step %d: size=%d evictions=%d states=%v", i, s.Count(), s.Evictions(), got)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
	t.Logf("model cross-check passed: %d steps, final size=%d evictions=%d",
		steps, s.Count(), s.Evictions())
}
