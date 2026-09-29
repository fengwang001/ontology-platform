package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// ---------- 测试辅助 ----------

func mustStore(t *testing.T, configs []ViewConfig) *SnapshotStore {
	t.Helper()
	s, err := NewSnapshotStore(configs)
	if err != nil {
		t.Fatalf("NewSnapshotStore: %v", err)
	}
	return s
}

func mustApply(t *testing.T, s *SnapshotStore, view string, ts Timestamp, value any) {
	t.Helper()
	if err := s.Apply(view, ts, value); err != nil {
		t.Fatalf("Apply(%s, %d): %v", view, ts, err)
	}
	t.Logf("apply view=%s ts=%d value=%v -> ok (minProgress=%d)", view, ts, value, s.MinProgress())
}

func mustHeartbeat(t *testing.T, s *SnapshotStore, view string, ts Timestamp) {
	t.Helper()
	if err := s.Heartbeat(view, ts); err != nil {
		t.Fatalf("Heartbeat(%s, %d): %v", view, ts, err)
	}
	t.Logf("heartbeat view=%s ts=%d -> ok (minProgress=%d)", view, ts, s.MinProgress())
}

func mustRead(t *testing.T, s *SnapshotStore, ts Timestamp) Snapshot {
	t.Helper()
	snap, err := s.Read(ts)
	if err != nil {
		t.Fatalf("Read(%d): %v", ts, err)
	}
	t.Logf("read ts=%d -> ok values=%v (minProgress=%d)", ts, snap.Values, s.MinProgress())
	return snap
}

// storeState 捕获内部状态，用于验证“失败不留痕”。
type storeState struct {
	progress map[string]Timestamp
	versions map[string][]Version
	lastRead Timestamp
}

func captureState(s *SnapshotStore) storeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := storeState{
		progress: make(map[string]Timestamp, len(s.views)),
		versions: make(map[string][]Version, len(s.views)),
		lastRead: s.lastRead,
	}
	for name, v := range s.views {
		st.progress[name] = v.progress
		st.versions[name] = append([]Version(nil), v.versions...)
	}
	return st
}

// ---------- 非法配置 ----------

func TestNewSnapshotStoreInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		configs []ViewConfig
	}{
		{"empty", nil},
		{"empty-name", []ViewConfig{{Name: "", Retention: 1}}},
		{"zero-retention", []ViewConfig{{Name: "a", Retention: 0}}},
		{"duplicate", []ViewConfig{{Name: "a", Retention: 1}, {Name: "a", Retention: 2}}},
	}
	for _, tc := range cases {
		_, err := NewSnapshotStore(tc.configs)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: want ErrInvalidArgument, got %v", tc.name, err)
		}
		t.Logf("config %s -> rejected: %v", tc.name, err)
	}
}

// ---------- 进度最小值 ----------

func TestReadAtMinProgressAndNotReady(t *testing.T) {
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 3}, {Name: "b", Retention: 3}})
	mustApply(t, s, "a", 1, "a1")
	mustApply(t, s, "a", 3, "a3")
	mustApply(t, s, "b", 2, "b2")
	mustApply(t, s, "b", 5, "b5")

	if got := s.MinProgress(); got != 3 {
		t.Fatalf("MinProgress = %d, want 3", got)
	}
	if _, err := s.Read(4); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Read(4): want ErrNotReady, got %v", err)
	}
	t.Logf("read ts=4 -> rejected: 超过进度最小值 3 (ErrNotReady)")

	snap := mustRead(t, s, 3)
	want := map[string]any{"a": "a3", "b": "b2"}
	if !reflect.DeepEqual(snap.Values, want) {
		t.Fatalf("Read(3) = %v, want %v", snap.Values, want)
	}

	mustHeartbeat(t, s, "b", 6)
	mustApply(t, s, "a", 7, "a7")
	snap = mustRead(t, s, 6)
	want = map[string]any{"a": "a3", "b": "b5"}
	if !reflect.DeepEqual(snap.Values, want) {
		t.Fatalf("Read(6) = %v, want %v", snap.Values, want)
	}
}

// ---------- 边界时间点 ----------

func TestReadBoundaryTimestamps(t *testing.T) {
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 2}, {Name: "b", Retention: 2}})
	mustApply(t, s, "a", 5, "x")
	mustApply(t, s, "a", 10, "y")
	mustApply(t, s, "b", 7, "p")
	mustApply(t, s, "b", 12, "q")

	// 恰好落在版本时间点上。
	if snap := mustRead(t, s, 7); !reflect.DeepEqual(snap.Values, map[string]any{"a": "x", "b": "p"}) {
		t.Fatalf("Read(7) = %v", snap.Values)
	}
	// 两个版本之间：取不超过该点的最大版本。
	if snap := mustRead(t, s, 9); !reflect.DeepEqual(snap.Values, map[string]any{"a": "x", "b": "p"}) {
		t.Fatalf("Read(9) = %v", snap.Values)
	}
	// 恰好等于进度最小值。
	if snap := mustRead(t, s, 10); !reflect.DeepEqual(snap.Values, map[string]any{"a": "y", "b": "p"}) {
		t.Fatalf("Read(10) = %v", snap.Values)
	}
	// 超过进度最小值一个单位即未准备好。
	if _, err := s.Read(11); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Read(11): want ErrNotReady, got %v", err)
	}
	t.Logf("read ts=11 -> rejected: 超过进度最小值 10 (ErrNotReady)")

	mustHeartbeat(t, s, "b", 20)
	mustApply(t, s, "a", 15, "z")
	if snap := mustRead(t, s, 12); !reflect.DeepEqual(snap.Values, map[string]any{"a": "y", "b": "q"}) {
		t.Fatalf("Read(12) = %v", snap.Values)
	}
}

// ---------- 淘汰导致太旧 ----------

func TestTooOldAfterEviction(t *testing.T) {
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 2}, {Name: "b", Retention: 1}})
	mustApply(t, s, "a", 1, "a1")
	mustApply(t, s, "a", 2, "a2")
	mustApply(t, s, "a", 3, "a3") // a 淘汰 a1，保留 {2,3}
	mustApply(t, s, "b", 1, "b1")
	mustApply(t, s, "b", 2, "b2") // b 淘汰 b1，保留 {2}

	// 已被淘汰的时间点不可读：a 最旧保留版本为 2，晚于 1。
	if _, err := s.Read(1); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Read(1): want ErrTooOld, got %v", err)
	}
	t.Logf("read ts=1 -> rejected: 视图 a 最旧保留版本为 2，晚于 1 (ErrTooOld)")

	// 边界：恰好等于最旧保留版本，仍可读。
	snap := mustRead(t, s, 2)
	if !reflect.DeepEqual(snap.Values, map[string]any{"a": "a2", "b": "b2"}) {
		t.Fatalf("Read(2) = %v", snap.Values)
	}

	mustApply(t, s, "b", 3, "b3") // b 淘汰 b2，保留 {3}
	if _, err := s.Read(2); !errors.Is(err, ErrTooOld) {
		t.Fatalf("Read(2): want ErrTooOld, got %v", err)
	}
	t.Logf("read ts=2 -> rejected: 视图 b 最旧保留版本为 3，晚于 2 (ErrTooOld)")
}

// ---------- 非法输入与拒绝后状态不变 ----------

func TestRejectionsLeaveStateUnchanged(t *testing.T) {
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 1}, {Name: "b", Retention: 1}})
	mustApply(t, s, "a", 1, "a1")
	mustApply(t, s, "a", 2, "a2") // a 保留 {2}
	mustApply(t, s, "b", 1, "b1")
	mustApply(t, s, "b", 2, "b2") // b 保留 {2}

	before := captureState(s)

	rejections := []struct {
		what string
		err  error
		want error
	}{
		{"apply unknown view", s.Apply("ghost", 3, "v"), ErrInvalidArgument},
		{"apply ts=0", s.Apply("a", 0, "v"), ErrInvalidArgument},
		{"apply nil value", s.Apply("a", 3, nil), ErrInvalidArgument},
		{"apply ts==progress", s.Apply("a", 2, "v"), ErrNonMonotonicTimestamp},
		{"apply ts<progress", s.Apply("a", 1, "v"), ErrNonMonotonicTimestamp},
		{"heartbeat unknown view", s.Heartbeat("ghost", 3), ErrInvalidArgument},
		{"heartbeat ts=-1", s.Heartbeat("a", -1), ErrInvalidArgument},
		{"heartbeat ts<progress", s.Heartbeat("a", 1), ErrNonMonotonicTimestamp},
	}
	for _, r := range rejections {
		if !errors.Is(r.err, r.want) {
			t.Fatalf("%s: want %v, got %v", r.what, r.want, r.err)
		}
		t.Logf("%s -> rejected: %v", r.what, r.err)
	}

	readRejections := []struct {
		what string
		ts   Timestamp
		want error
	}{
		{"read ts=0", 0, ErrInvalidArgument},
		{"read beyond min progress", 50, ErrNotReady},
		{"read evicted ts", 1, ErrTooOld},
	}
	for _, r := range readRejections {
		_, err := s.Read(r.ts)
		if !errors.Is(err, r.want) {
			t.Fatalf("%s: want %v, got %v", r.what, r.want, err)
		}
		t.Logf("%s (ts=%d) -> rejected: %v", r.what, r.ts, err)
	}

	after := captureState(s)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejections:\nbefore=%+v\nafter=%+v", before, after)
	}
	t.Logf("所有拒绝均未改变进度、版本与保留情况")
}

// ---------- 读取时间点单调不减、同点结果不变 ----------

func TestReadTimestampsMonotonic(t *testing.T) {
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 5}, {Name: "b", Retention: 5}})
	mustApply(t, s, "a", 1, "a1")
	mustApply(t, s, "a", 5, "a5")
	mustApply(t, s, "b", 2, "b2")
	mustApply(t, s, "b", 6, "b6")

	first := mustRead(t, s, 3)
	second := mustRead(t, s, 3) // 同一时间点重复读，结果逐视图不变
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same ts read twice differs: %v vs %v", first, second)
	}

	if _, err := s.Read(2); !errors.Is(err, ErrNonMonotonicTimestamp) {
		t.Fatalf("Read(2) after Read(3): want ErrNonMonotonicTimestamp, got %v", err)
	}
	t.Logf("read ts=2 -> rejected: 早于上次成功读取的时间点 3 (ErrNonMonotonicTimestamp)")

	third := mustRead(t, s, 5)
	if !reflect.DeepEqual(third.Values, map[string]any{"a": "a5", "b": "b2"}) {
		t.Fatalf("Read(5) = %v", third.Values)
	}
	if _, err := s.Read(4); !errors.Is(err, ErrNonMonotonicTimestamp) {
		t.Fatalf("Read(4) after Read(5): want ErrNonMonotonicTimestamp, got %v", err)
	}
	t.Logf("read ts=4 -> rejected: 早于上次成功读取的时间点 5 (ErrNonMonotonicTimestamp)")
}

// ---------- 与保留全历史的朴素参照一致 ----------

// naiveRef 朴素参照：保留全历史、不做任何淘汰。
type naiveRef struct {
	versions map[string][]Version
}

func newNaiveRef(names []string) *naiveRef {
	n := &naiveRef{versions: make(map[string][]Version, len(names))}
	for _, name := range names {
		n.versions[name] = nil
	}
	return n
}

func (n *naiveRef) apply(view string, ts Timestamp, value any) {
	n.versions[view] = append(n.versions[view], Version{TS: ts, Value: value})
}

func (n *naiveRef) valueAt(view string, ts Timestamp) (any, bool) {
	var best *Version
	for i := range n.versions[view] {
		v := &n.versions[view][i]
		if v.TS <= ts && (best == nil || v.TS > best.TS) {
			best = v
		}
	}
	if best == nil {
		return nil, false
	}
	return best.Value, true
}

func (n *naiveRef) snapshot(names []string, ts Timestamp) map[string]any {
	out := make(map[string]any, len(names))
	for _, name := range names {
		if v, ok := n.valueAt(name, ts); ok {
			out[name] = v
		}
	}
	return out
}

func TestAgainstNaiveReference(t *testing.T) {
	names := []string{"a", "b", "c"}
	s := mustStore(t, []ViewConfig{{Name: "a", Retention: 1}, {Name: "b", Retention: 2}, {Name: "c", Retention: 3}})
	ref := newNaiveRef(names)
	rng := rand.New(rand.NewSource(42))

	progress := map[string]Timestamp{"a": 0, "b": 0, "c": 0}
	var lastRead Timestamp
	step := 0
	logDecision := func(format string, args ...any) {
		t.Logf("step %d: %s", step, fmt.Sprintf(format, args...))
	}

	for step = 1; step <= 300; step++ {
		view := names[rng.Intn(len(names))]
		switch rng.Intn(3) {
		case 0: // apply
			ts := progress[view] + Timestamp(1+rng.Intn(3))
			value := fmt.Sprintf("%s@%d", view, ts)
			if err := s.Apply(view, ts, value); err != nil {
				t.Fatalf("step %d: Apply(%s, %d): %v", step, view, ts, err)
			}
			ref.apply(view, ts, value)
			progress[view] = ts
			logDecision("apply view=%s ts=%d -> ok (minProgress=%d)", view, ts, s.MinProgress())
		case 1: // heartbeat
			ts := progress[view] + Timestamp(1+rng.Intn(3))
			if err := s.Heartbeat(view, ts); err != nil {
				t.Fatalf("step %d: Heartbeat(%s, %d): %v", step, view, ts, err)
			}
			progress[view] = ts
			logDecision("heartbeat view=%s ts=%d -> ok (minProgress=%d)", view, ts, s.MinProgress())
		default: // read
			minP := s.MinProgress()
			if minP <= lastRead {
				step--
				continue
			}
			ts := lastRead + Timestamp(rng.Intn(int(minP-lastRead)+2))
			if ts < 1 {
				ts = 1
			}
			snap, err := s.Read(ts)
			switch {
			case errors.Is(err, ErrNotReady):
				if ts <= minP {
					t.Fatalf("step %d: Read(%d) ErrNotReady but minProgress=%d", step, ts, minP)
				}
				logDecision("read ts=%d -> ErrNotReady (minProgress=%d)", ts, minP)
			case errors.Is(err, ErrTooOld):
				st := captureState(s)
				justified := false
				for _, name := range names {
					if len(st.versions[name]) == 0 || st.versions[name][0].TS > ts {
						justified = true
					}
				}
				if !justified {
					t.Fatalf("step %d: Read(%d) ErrTooOld 但所有视图最旧保留版本均不晚于该点", step, ts)
				}
				logDecision("read ts=%d -> ErrTooOld (minProgress=%d, 依据: 存在视图最旧保留版本晚于该点)", ts, minP)
			case err != nil:
				t.Fatalf("step %d: Read(%d): %v", step, ts, err)
			default:
				want := ref.snapshot(names, ts)
				if !reflect.DeepEqual(snap.Values, want) {
					t.Fatalf("step %d: Read(%d) = %v, naive ref = %v", step, ts, snap.Values, want)
				}
				lastRead = ts
				logDecision("read ts=%d -> ok values=%v 与朴素参照一致 (minProgress=%d)", ts, snap.Values, minP)
			}
		}
	}
}

// ---------- 并发：多读多写 ----------

func TestConcurrentReadApplyHeartbeat(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	s := mustStore(t, []ViewConfig{
		{Name: "a", Retention: 4}, {Name: "b", Retention: 4},
		{Name: "c", Retention: 4}, {Name: "d", Retention: 4},
	})

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(view string) {
			defer wg.Done()
			for i := 1; i <= 200; i++ {
				ts := Timestamp(i)
				if i%3 == 0 {
					if err := s.Heartbeat(view, ts); err != nil {
						t.Errorf("Heartbeat(%s, %d): %v", view, ts, err)
						return
					}
					continue
				}
				if err := s.Apply(view, ts, fmt.Sprintf("%s@%d", view, ts)); err != nil {
					t.Errorf("Apply(%s, %d): %v", view, ts, err)
					return
				}
			}
		}(name)
	}

	// 记录同一时间点的读取结果，校验“同点结果逐视图不变”。
	var mu sync.Mutex
	seen := make(map[Timestamp]Snapshot)

	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			var last Timestamp
			for i := 0; i < 500; i++ {
				ts := s.MinProgress()
				if ts == 0 {
					continue
				}
				snap, err := s.Read(ts)
				if errors.Is(err, ErrNonMonotonicTimestamp) || errors.Is(err, ErrTooOld) {
					// 采样后游标被其他读者推进，或该点版本已被淘汰：重采样重试。
					continue
				}
				if err != nil {
					t.Errorf("reader %d: Read(%d): %v", reader, ts, err)
					return
				}
				if snap.TS < last {
					t.Errorf("reader %d: read timestamps decreased: %d -> %d", reader, last, snap.TS)
					return
				}
				last = snap.TS
				mu.Lock()
				if prev, ok := seen[snap.TS]; ok && !reflect.DeepEqual(prev.Values, snap.Values) {
					mu.Unlock()
					t.Errorf("reader %d: same ts %d read differs: %v vs %v", reader, snap.TS, prev.Values, snap.Values)
					return
				}
				seen[snap.TS] = snap
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	t.Logf("并发完成：%d 个不同时间点被读取并交叉校验一致", len(seen))
}
