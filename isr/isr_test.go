package isr

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// refRS 是朴素参照实现：用最直白的方式重述同一套 ISR/高水位规则。
type refRS struct {
	leader string
	leo    map[string]int64
	caught map[string]int64
	isr    map[string]struct{}
	hw     int64
	maxTs  int64
}

func newRef(leader string) *refRS {
	return &refRS{
		leader: leader,
		leo:    map[string]int64{leader: 0},
		caught: map[string]int64{leader: 0},
		isr:    map[string]struct{}{leader: {}},
	}
}

func (r *refRS) recomputeHW() {
	minLEO := r.leo[r.leader]
	for id := range r.isr {
		if r.leo[id] < minLEO {
			minLEO = r.leo[id]
		}
	}
	if minLEO > r.hw {
		r.hw = minLEO
	}
}

func (r *refRS) checkClock(now int64) error {
	if now < 0 {
		return ErrInvalidArgument
	}
	if now < r.maxTs {
		return ErrClockWentBack
	}
	return nil
}

func (r *refRS) add(id string) error {
	if id == "" || id == r.leader {
		return ErrInvalidArgument
	}
	if _, ok := r.leo[id]; ok {
		return ErrInvalidArgument
	}
	r.leo[id] = 0
	r.caught[id] = 0
	if r.hw == 0 {
		r.isr[id] = struct{}{}
	}
	return nil
}

func (r *refRS) append1(newLEO, now int64) error {
	if err := r.checkClock(now); err != nil {
		return err
	}
	if newLEO <= r.leo[r.leader] {
		return ErrInvalidOffset
	}
	r.leo[r.leader] = newLEO
	r.maxTs = now
	r.recomputeHW()
	return nil
}

func (r *refRS) fetch(id string, offset, now int64) error {
	if id == "" || id == r.leader {
		return ErrInvalidArgument
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	if _, ok := r.leo[id]; !ok {
		return ErrUnknownReplica
	}
	if offset < 0 || offset < r.leo[id] || offset > r.leo[r.leader] {
		return ErrInvalidOffset
	}
	r.leo[id] = offset
	if offset >= r.hw {
		r.caught[id] = now
		r.isr[id] = struct{}{}
	}
	r.maxTs = now
	r.recomputeHW()
	return nil
}

func (r *refRS) check(now, tolerance int64) error {
	if tolerance < 0 {
		return ErrInvalidArgument
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	for id := range r.isr {
		if id == r.leader {
			continue
		}
		if now-r.caught[id] > tolerance {
			delete(r.isr, id)
		}
	}
	r.maxTs = now
	r.recomputeHW()
	return nil
}

func (r *refRS) query(now int64) error {
	if err := r.checkClock(now); err != nil {
		return err
	}
	r.maxTs = now
	return nil
}

func newTestRS(t *testing.T) (*ReplicaSet, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	rs, err := NewReplicaSet("L", log.New(&buf, "", 0))
	if err != nil {
		t.Fatalf("NewReplicaSet: %v", err)
	}
	return rs, &buf
}

func sameErr(got, want error) bool {
	return errors.Is(got, want)
}

func assertSame(t *testing.T, rs *ReplicaSet, ref *refRS, where string) {
	t.Helper()
	snap, err := rs.Query(ref.maxTs)
	if err != nil {
		t.Fatalf("%s: Query: %v", where, err)
	}
	if snap.HighWatermark != ref.hw {
		t.Fatalf("%s: hw=%d want %d", where, snap.HighWatermark, ref.hw)
	}
	wantISR := make([]string, 0, len(ref.isr))
	for id := range ref.isr {
		wantISR = append(wantISR, id)
	}
	sort.Strings(wantISR)
	if fmt.Sprint(snap.ISR) != fmt.Sprint(wantISR) {
		t.Fatalf("%s: isr=%v want %v", where, snap.ISR, wantISR)
	}
	if len(snap.Progress) != len(ref.leo) {
		t.Fatalf("%s: progress size=%d want %d", where, len(snap.Progress), len(ref.leo))
	}
	for id, wantLEO := range ref.leo {
		got := snap.Progress[id]
		if got.LEO != wantLEO || got.LastCaughtUp != ref.caught[id] {
			t.Fatalf("%s: progress[%s]=%+v want leo=%d caught=%d",
				where, id, got, wantLEO, ref.caught[id])
		}
	}
}

// TestRemoveAndRejoin 覆盖：追平进入、严格大于阈值才剔除、追平当前高水位重新加入。
func TestRemoveAndRejoin(t *testing.T) {
	rs, _ := newTestRS(t)
	ref := newRef("L")

	must := func(where string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
	}

	must("add a", rs.AddReplica("a"))
	must("add b", rs.AddReplica("b"))
	ref.add("a")
	ref.add("b")

	must("append 5@1", rs.Append(5, 1))
	ref.append1(5, 1)
	must("a fetch 5@2", rs.Fetch("a", 5, 2))
	ref.fetch("a", 5, 2)
	must("b fetch 3@2", rs.Fetch("b", 3, 2))
	ref.fetch("b", 3, 2)
	assertSame(t, rs, ref, "after fetches")

	// lag == tolerance（3-2=1 <= 1）不剔除：严格大于才剔除。
	must("check equal-lag keeps both", rs.PeriodicCheck(3, 1))
	ref.check(3, 1)
	assertSame(t, rs, ref, "lag==tolerance")

	// b 未追平高水位 5，now-lastCaughtUp=3 > tolerance 2 => 剔除 b。
	must("check removes b", rs.PeriodicCheck(3, 2))
	ref.check(3, 2)
	assertSame(t, rs, ref, "b removed")

	// 领导者推进到 10；ISR={L,a}，min(10,5)=5，高水位不退。
	must("append 10@4", rs.Append(10, 4))
	ref.append1(10, 4)
	assertSame(t, rs, ref, "leader ahead of shrunk ISR")

	// b 追平当前高水位 5（无需追到领导者 LEO 10）即可重新加入。
	must("b rejoin at hw", rs.Fetch("b", 5, 5))
	ref.fetch("b", 5, 5)
	assertSame(t, rs, ref, "b rejoined")

	// 全员追平 10 后高水位推进到 10。
	must("a fetch 10@6", rs.Fetch("a", 10, 6))
	ref.fetch("a", 10, 6)
	must("b fetch 10@7", rs.Fetch("b", 10, 7))
	ref.fetch("b", 10, 7)
	assertSame(t, rs, ref, "all caught")
	if ref.hw != 10 {
		t.Fatalf("hw=%d want 10", ref.hw)
	}

	// 领导者始终在 ISR 中。
	snap, _ := rs.Query(7)
	if _, ok := indexOf(snap.ISR, "L"); !ok {
		t.Fatalf("leader missing from ISR: %v", snap.ISR)
	}
}

func indexOf(xs []string, x string) (int, bool) {
	for i, v := range xs {
		if v == x {
			return i, true
		}
	}
	return 0, false
}

// TestErrorCategories 校验四类拒绝原因互不相同、可用 errors.Is 区分。
func TestErrorCategories(t *testing.T) {
	sentinels := []error{ErrInvalidArgument, ErrUnknownReplica, ErrInvalidOffset, ErrClockWentBack}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}

	rs, _ := newTestRS(t)
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"new empty leader", mustNew("", nil), ErrInvalidArgument},
		{"append non-advance", rs.Append(0, 1), ErrInvalidOffset},
		{"fetch unknown", rs.Fetch("ghost", 0, 1), ErrUnknownReplica},
		{"fetch leader", rs.Fetch("L", 0, 1), ErrInvalidArgument},
		{"add empty", rs.AddReplica(""), ErrInvalidArgument},
		{"add leader id", rs.AddReplica("L"), ErrInvalidArgument},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, tc.err, tc.want)
		}
	}

	if err := rs.AddReplica("a"); err != nil {
		t.Fatal(err)
	}
	if err := rs.AddReplica("a"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate follower: %v", err)
	}
	if err := rs.Append(5, 10); err != nil {
		t.Fatal(err)
	}
	if err := rs.Fetch("a", 6, 11); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("offset beyond leader: %v", err)
	}
	if err := rs.Fetch("a", -1, 11); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("negative offset: %v", err)
	}
	if err := rs.Fetch("a", 3, 9); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("clock rollback on fetch: %v", err)
	}
	if err := rs.Append(6, 9); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("clock rollback on append: %v", err)
	}
	if err := rs.PeriodicCheck(11, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative tolerance: %v", err)
	}
	if err := rs.PeriodicCheck(0, 0); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("clock rollback on check: %v", err)
	}
}

func mustNew(leaderID string, logger *log.Logger) error {
	_, err := NewReplicaSet(leaderID, logger)
	return err
}

// TestRejectionLeavesNoTrace 任何被拒调用前后快照完全一致。
func TestRejectionLeavesNoTrace(t *testing.T) {
	rs, _ := newTestRS(t)
	if err := rs.AddReplica("a"); err != nil {
		t.Fatal(err)
	}
	if err := rs.Append(5, 1); err != nil {
		t.Fatal(err)
	}
	if err := rs.Fetch("a", 5, 2); err != nil {
		t.Fatal(err)
	}

	before, err := rs.Query(2)
	if err != nil {
		t.Fatal(err)
	}
	badCalls := []func() error{
		func() error { return rs.Append(3, 3) },
		func() error { return rs.Append(6, 0) },
		func() error { return rs.Fetch("a", 4, 3) },
		func() error { return rs.Fetch("a", 6, 3) },
		func() error { return rs.Fetch("ghost", 0, 3) },
		func() error { return rs.Fetch("L", 0, 3) },
		func() error { return rs.PeriodicCheck(3, -1) },
		func() error { return rs.PeriodicCheck(0, 0) },
		func() error { _, e := rs.Query(0); return e },
		func() error { return rs.AddReplica("a") },
	}
	for i, call := range badCalls {
		if err := call(); err == nil {
			t.Fatalf("bad call #%d unexpectedly succeeded", i)
		}
		after, err := rs.Query(2)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(after) != fmt.Sprint(before) {
			t.Fatalf("bad call #%d changed state:\nbefore=%+v\nafter =%+v", i, before, after)
		}
	}
}

// TestRandomizedAgainstReference 随机操作流逐步与朴素参照比对（含非法输入与拒绝）。
func TestRandomizedAgainstReference(t *testing.T) {
	rs, logBuf := newTestRS(t)
	ref := newRef("L")
	rng := rand.New(rand.NewSource(42))

	known := []string{"f0", "f1", "f2", "f3"}
	clock := int64(0)
	leaderLEO := int64(0)

	register := func(id string) {
		got, want := rs.AddReplica(id), ref.add(id)
		if !sameErr(got, want) {
			t.Fatalf("add %s: got %v want %v", id, got, want)
		}
	}
	for _, id := range known {
		register(id)
	}

	for step := 0; step < 4000; step++ {
		// 大多数步骤时间正常前进，少量故意回退以触发时钟拒绝。
		var now int64
		if rng.Intn(10) == 0 {
			now = clock - rng.Int63n(3) - 1
		} else {
			clock++
			now = clock
		}

		var got, want error
		switch rng.Intn(6) {
		case 0:
			leaderLEO++
			got, want = rs.Append(leaderLEO, now), ref.append1(leaderLEO, now)
			if !sameErr(got, want) {
				t.Fatalf("step %d append(%d,%d): got %v want %v", step, leaderLEO, now, got, want)
			}
			if want != nil {
				leaderLEO--
			}
		case 1, 2, 3:
			id := known[rng.Intn(len(known))]
			// 位点在 [-1, leaderLEO+2] 内随机，可能回退/越界。
			offset := rng.Int63n(leaderLEO+4) - 1
			got, want = rs.Fetch(id, offset, now), ref.fetch(id, offset, now)
			if !sameErr(got, want) {
				t.Fatalf("step %d fetch(%s,%d,%d): got %v want %v", step, id, offset, now, got, want)
			}
		case 4:
			tol := rng.Int63n(4)
			if rng.Intn(8) == 0 {
				tol = -1
			}
			got, want = rs.PeriodicCheck(now, tol), ref.check(now, tol)
			if !sameErr(got, want) {
				t.Fatalf("step %d check(%d,%d): got %v want %v", step, now, tol, got, want)
			}
		case 5:
			_, qerr := rs.Query(now)
			got, want = qerr, ref.query(now)
			if !sameErr(got, want) {
				t.Fatalf("step %d query(%d): got %v want %v", step, now, got, want)
			}
		}

		// 成功调用后真实时钟基准为参照时钟；拒绝不推进 maxTs。
		if want == nil && now > clock {
			clock = now
		}
		assertSame(t, rs, ref, fmt.Sprintf("step %d", step))
	}

	if logBuf.Len() == 0 {
		t.Fatal("expected step-by-step decision logs")
	}
}

// TestConcurrentSafety 并发追加/拉取/检查/查询下的竞态与不变量。
func TestConcurrentSafety(t *testing.T) {
	var logBuf bytes.Buffer
	rs, err := NewReplicaSet("L", log.New(&logBuf, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	const followers = 4
	for i := 0; i < followers; i++ {
		if err := rs.AddReplica(fmt.Sprintf("f%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var clock int64
	next := func() int64 { return atomic.AddInt64(&clock, 1) }

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 领导者持续追加。
	var leo int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				v := atomic.LoadInt64(&leo) + 1
				if err := rs.Append(v, next()); err == nil {
					atomic.StoreInt64(&leo, v)
				}
			}
		}
	}()

	// 每个跟随者持续拉取领导者当前 LEO（失败则重试，不回退自身进度）。
	for i := 0; i < followers; i++ {
		id := fmt.Sprintf("f%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = rs.Fetch(id, atomic.LoadInt64(&leo), next())
				}
			}
		}()
	}

	// 周期性检查，容忍阈值很小，制造 ISR 抖动。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = rs.PeriodicCheck(next(), 1)
			}
		}
	}()

	// 查询者持续校验不变量。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap, err := rs.Query(next())
				if err != nil {
					continue
				}
				if _, ok := indexOf(snap.ISR, "L"); !ok {
					t.Errorf("leader missing from ISR %v", snap.ISR)
					return
				}
				for _, id := range snap.ISR {
					if snap.Progress[id].LEO < snap.HighWatermark {
						t.Errorf("hw %d exceeds ISR progress of %s (%d)",
							snap.HighWatermark, id, snap.Progress[id].LEO)
						return
					}
				}
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 收尾：所有跟随者追平，检查不再剔除，高水位应到达最终 LEO。
	final := atomic.LoadInt64(&leo)
	now := next()
	for i := 0; i < followers; i++ {
		if err := rs.Fetch(fmt.Sprintf("f%d", i), final, now); err != nil {
			t.Fatal(err)
		}
		now++
	}
	if err := rs.PeriodicCheck(now, 1<<40); err != nil {
		t.Fatal(err)
	}
	snap, err := rs.Query(now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.HighWatermark != final {
		t.Fatalf("final hw=%d want %d; ISR=%v", snap.HighWatermark, final, snap.ISR)
	}
}
