package ontology

import (
	"sync"
	"testing"
)

func newTestStore() (*Store, *FakeClock) {
	clk := &FakeClock{}
	return NewStore(clk), clk
}

// 普通属性更新走乐观规则：版本匹配才提交，提交后版本 +1。
func TestOptimisticBasic(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", Props{"a": "1"})

	if got := s.Update("u", "o1", 1, Patch{"a": "2"}); got != OutcomeCommitted {
		t.Fatalf("update = %v, want committed", got)
	}
	snap, _ := s.Snapshot("o1")
	if snap.Version != 2 || snap.Props["a"] != "2" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// 旧版本号再次提交：版本落后，且不得改变状态。
	if got := s.Update("u", "o1", 1, Patch{"a": "9"}); got != OutcomeVersionStale {
		t.Fatalf("stale update = %v, want version-stale", got)
	}
	snap, _ = s.Snapshot("o1")
	if snap.Version != 2 || snap.Props["a"] != "2" {
		t.Fatalf("rejected update mutated state: %+v", snap)
	}
}

// 占用申请在已被占用时必须立即收到可单独识别的“已被占用”拒绝，
// 不阻塞、不排队；占用期间的普通更新被单独类别拒绝且版本不变。
func TestAcquireRejectAndOccupiedUpdate(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", nil)

	l1, got := s.TryAcquire("act-A", []string{"o1"}, 10_000)
	if got != OutcomeCommitted {
		t.Fatalf("first acquire = %v", got)
	}
	l2, got := s.TryAcquire("act-B", []string{"o1"}, 10_000)
	if l2 != nil || got != OutcomeOccupied {
		t.Fatalf("second acquire = (%v,%v), want nil,Occupied", l2, got)
	}

	// 占用期间普通更新：专门类别，版本不变。
	before, _ := s.Snapshot("o1")
	if got := s.Update("u", "o1", before.Version, Patch{"a": "x"}); got != OutcomeOptimisticRejected {
		t.Fatalf("occupied update = %v, want optimistic-rejected", got)
	}
	after, _ := s.Snapshot("o1")
	if after.Version != before.Version {
		t.Fatalf("version changed during occupancy: %d -> %d", before.Version, after.Version)
	}

	// 释放后旧版本号仍按最新版本重新判定：版本落后而不是占用拒绝。
	if got := l1.Release(); got != OutcomeCommitted {
		t.Fatalf("release = %v", got)
	}
	if got := s.Update("u", "o1", before.Version-1, Patch{"a": "x"}); got != OutcomeVersionStale {
		t.Fatalf("post-release old-version update = %v, want stale", got)
	}
	if got := s.Update("u", "o1", before.Version, Patch{"a": "x"}); got != OutcomeCommitted {
		t.Fatalf("post-release fresh update = %v, want committed", got)
	}
}

// 占用期间外部读取只能看到占用前的完整状态；提交与释放在同一原子事件发布。
func TestAtomicVisibility(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "init", Props{"a": "0", "b": "0"})
	l, _ := s.TryAcquire("act", []string{"o1"}, 10_000)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap, _ := s.Snapshot("o1")
				// 任何一次读取都必须是“占用前完整状态”或“占用后完整状态”，
				// 绝不允许 a/b 半更新的中间组合。
				switch {
				case snap.State == "init" && snap.Props["a"] == "0" && snap.Props["b"] == "0":
				case snap.State == "done" && snap.Props["a"] == "1" && snap.Props["b"] == "1":
				default:
					panic("observed intermediate state: " + snap.State +
						" a=" + snap.Props["a"] + " b=" + snap.Props["b"])
				}
			}
		}
	}()

	if got := l.Stage("o1", "done", Patch{"a": "1", "b": "1"}, nil); got != OutcomeCommitted {
		t.Fatalf("stage = %v", got)
	}
	// 暂存期间读取仍应是占用前状态。
	if snap, _ := s.Snapshot("o1"); snap.State != "init" || snap.Props["a"] != "0" {
		t.Fatalf("staged changes leaked: %+v", snap)
	}
	if got := l.Commit(); got != OutcomeCommitted {
		t.Fatalf("commit = %v", got)
	}
	close(stop)
	wg.Wait()

	snap, _ := s.Snapshot("o1")
	if snap.State != "done" || snap.Props["a"] != "1" || snap.Props["b"] != "1" || snap.Version != 2 {
		t.Fatalf("post-commit snapshot = %+v", snap)
	}
}

// 跨链接的多实例占用：无论申请方以何种顺序给出键，系统都按全局规范序
// 原子授予或整体拒绝；冲突中必然有且只有一方被拒绝，结果由判定顺序确定。
func TestCrossInstanceLockOrdering(t *testing.T) {
	cases := [][][]string{
		{{"A", "B"}, {"B", "A"}},
		{{"A", "B"}, {"A", "B"}},
		{{"B", "A", "C"}, {"A", "C", "B"}},
	}
	for ci, tc := range cases {
		s, _ := newTestStore()
		for _, k := range []string{"A", "B", "C"} {
			s.CreateInstance(k, "open", nil)
		}
		l1, got1 := s.TryAcquire("act-1", tc[0], 10_000)
		l2, got2 := s.TryAcquire("act-2", tc[1], 10_000)
		if got1 != OutcomeCommitted || l1 == nil {
			t.Fatalf("case %d: first acquire = %v", ci, got1)
		}
		if l2 != nil || got2 != OutcomeLockConflict {
			t.Fatalf("case %d: second acquire = (%v,%v), want LockConflict", ci, l2, got2)
		}
		// 第二方不得持有任何一个所申请实例上的占用。
		for _, k := range tc[1] {
			if snap, _ := s.Snapshot(k); snap.Holder != "act-1" {
				t.Fatalf("case %d: key %s holder = %q, want act-1 (no partial hold)", ci, k, snap.Holder)
			}
		}
		l1.Release()
		l3, got3 := s.TryAcquire("act-2", tc[1], 10_000)
		if got3 != OutcomeCommitted || l3 == nil {
			t.Fatalf("case %d: acquire after release = %v", ci, got3)
		}
		l3.Release()
	}
}

// 同一方不能靠再申请造成重入叠加；占用是单层的。
func TestNoReentrantAcquire(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", nil)
	l, got := s.TryAcquire("act", []string{"o1"}, 10_000)
	if got != OutcomeCommitted {
		t.Fatal(got)
	}
	if _, got := s.TryAcquire("act", []string{"o1"}, 10_000); got != OutcomeOccupied {
		t.Fatalf("reentrant acquire = %v, want Occupied", got)
	}
	l.Release()
}

// 两类占用拒绝与版本落后必须互斥可辨，且占用判定先于版本判定。
func TestOutcomeDistinguishabilityAndOrder(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", nil)
	s.CreateInstance("o2", "open", nil)
	l, _ := s.TryAcquire("act", []string{"o1"}, 10_000)

	// 版本号故意给错：占用判定在前，仍应返回占用拒绝而非版本落后。
	if got := s.Update("u", "o1", 999, nil); got != OutcomeOptimisticRejected {
		t.Fatalf("occupied+stale = %v, want optimistic-rejected first", got)
	}
	l.Release()

	_, got := s.TryAcquire("act2", []string{"o1", "o2"}, 10_000)
	if got != OutcomeCommitted {
		t.Fatal(got)
	}
	if _, got := s.TryAcquire("act3", []string{"o1"}, 10_000); got != OutcomeOccupied {
		t.Fatalf("single = %v, want Occupied", got)
	}
	if _, got := s.TryAcquire("act4", []string{"o1", "o2"}, 10_000); got != OutcomeLockConflict {
		t.Fatalf("multi = %v, want LockConflict", got)
	}
}

// 基数冲突是与其它拒绝原因互不相同的类别，且占用/版本判定先于基数判定。
func TestCardinalityConflict(t *testing.T) {
	s, _ := newTestStore()
	s.AddLinkType(LinkType{Name: "ref", SourceKey: "o1", TargetKey: "t", MaxOut: 1})
	s.CreateInstance("o1", "open", nil)
	s.CreateInstance("t1", "open", nil)
	s.CreateInstance("t2", "open", nil)

	if got := s.Link("u", "o1", 1, "ref", "t1"); got != OutcomeCommitted {
		t.Fatal(got)
	}
	if got := s.Link("u", "o1", 2, "ref", "t2"); got != OutcomeCardinality {
		t.Fatalf("over cardinality = %v, want Cardinality", got)
	}

	l, _ := s.TryAcquire("act", []string{"o1"}, 10_000)
	// 占用期间尝试超限链接：先返回占用拒绝。
	if got := s.Link("u", "o1", 2, "ref", "t2"); got != OutcomeOptimisticRejected {
		t.Fatalf("occupied link = %v, want optimistic-rejected", got)
	}
	l.Release()
}

// 占用刚释放瞬间的更新竞争：大量更新并发提交，恰好一个按当时最新版本成功，
// 其余看到更新后的版本而得到版本落后——没有一个因排队而误成功。
func TestReleaseInstantRace(t *testing.T) {
	s, _ := newTestStore()
	s.CreateInstance("o1", "open", nil)
	l, _ := s.TryAcquire("act", []string{"o1"}, 10_000)

	const n = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]Outcome, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = s.Update("u", "o1", 1, Patch{"k": "v"})
		}(i)
	}
	if got := l.Release(); got != OutcomeCommitted {
		t.Fatal(got)
	}
	close(start)
	wg.Wait()

	committed, stale, occupied := 0, 0, 0
	for _, r := range results {
		switch r {
		case OutcomeCommitted:
			committed++
		case OutcomeVersionStale:
			stale++
		case OutcomeOptimisticRejected:
			occupied++
		}
	}
	if committed != 1 {
		t.Fatalf("committed = %d, want exactly 1", committed)
	}
	if stale != n-1 {
		t.Fatalf("stale = %d, want %d; occupied=%d", stale, n-1, occupied)
	}
	snap, _ := s.Snapshot("o1")
	if snap.Version != 2 {
		t.Fatalf("final version = %d, want 2", snap.Version)
	}
}
