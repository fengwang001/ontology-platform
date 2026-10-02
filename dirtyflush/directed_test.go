package dirtyflush

import (
	"sync"
	"testing"
)

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Modify 落在已脏、非在途页上：p.lsn 推进但 oldest 不变。
func TestModifyDirtyKeepsOldest(t *testing.T) {
	m, _ := New(10)
	mustOK(t, m.Modify(5, 100), "")
	mustOK(t, m.Modify(5, 120), "")
	pg, _ := m.Snapshot(5)
	if pg.Oldest != 100 || pg.LSN != 120 {
		t.Fatalf("page = %+v", pg)
	}
	if got := m.Order(); !eqInts(got, []int{5}) {
		t.Fatalf("order = %v", got)
	}
	assertInvariants(t, m)
}

// AddDep 对干净 a 不登记；重复登记幂等；间接环也拒绝。
func TestAddDepRules(t *testing.T) {
	m, _ := New(10)
	mustOK(t, m.Modify(2, 10), "")
	mustOK(t, m.AddDep(1, 2), "")
	if edges := m.OutEdges(1); edges != nil {
		t.Fatalf("clean a must not register edge, got %v", edges)
	}
	mustOK(t, m.Modify(1, 20), "")
	mustOK(t, m.AddDep(1, 2), "")
	mustOK(t, m.AddDep(1, 2), "idempotent")
	if edges := m.OutEdges(1); len(edges) != 1 || edges[0] != 2 {
		t.Fatalf("edges = %v", edges)
	}
	rejectIs(t, m.AddDep(2, 1), ReasonCycle, "direct cycle")
	mustOK(t, m.Modify(3, 30), "")
	mustOK(t, m.AddDep(3, 1), "")
	rejectIs(t, m.AddDep(2, 3), ReasonCycle, "indirect cycle")
	assertInvariants(t, m)
}

// FlushStart 四类拒绝的先后顺序：不脏 > 在途 > 未落盘 > 前置未刷。
func TestFlushStartRejectOrder(t *testing.T) {
	m, _ := New(10)
	_, err := m.FlushStart(7)
	rejectIs(t, err, ReasonPageNotDirty, "not dirty")

	mustOK(t, m.Modify(7, 100), "")
	_, err = m.FlushStart(7)
	rejectIs(t, err, ReasonLogNotDurable, "not durable")

	mustOK(t, m.SetFlushed(100), "")
	_, err = m.FlushStart(7)
	mustOK(t, err, "start once")
	_, err = m.FlushStart(7)
	rejectIs(t, err, ReasonPageInFlight, "in flight")
	mustOK(t, m.FlushDone(7), "")

	mustOK(t, m.Modify(1, 110), "")
	mustOK(t, m.Modify(2, 120), "")
	mustOK(t, m.AddDep(1, 2), "")
	mustOK(t, m.SetFlushed(120), "")
	_, err = m.FlushStart(2)
	rejectIs(t, err, ReasonPredecessorDirty, "pred dirty")

	_, err = m.FlushStart(1)
	mustOK(t, err, "start pred")
	_, err = m.FlushStart(2)
	rejectIs(t, err, ReasonPredecessorDirty, "pred in flight")

	_, err = m.FlushStart(MaxPage + 1)
	rejectIs(t, err, ReasonInvalidArg, "bad page")
	assertInvariants(t, m)
}

// 日志水位恰等于 p.lsn 允许，少 1 拒绝；SetFlushed 相等允许、回退拒绝。
func TestFlushedBoundary(t *testing.T) {
	m, _ := New(10)
	mustOK(t, m.Modify(4, 50), "")
	mustOK(t, m.SetFlushed(49), "")
	_, err := m.FlushStart(4)
	rejectIs(t, err, ReasonLogNotDurable, "49 < 50")
	mustOK(t, m.SetFlushed(50), "equal water mark allowed")
	_, err = m.FlushStart(4)
	mustOK(t, err, "lsn == flushed")
	mustOK(t, m.SetFlushed(50), "same value allowed")
	rejectIs(t, m.SetFlushed(40), ReasonLSNTooSmall, "rollback")
	assertInvariants(t, m)
}

// Checkpoint 链表为空时 = 最大 Modify LSN + 1；从未 Modify 为 1。
func TestCheckpointEmpty(t *testing.T) {
	m, _ := New(10)
	if cp := m.Checkpoint(); cp != 1 {
		t.Fatalf("initial checkpoint = %d", cp)
	}
	mustOK(t, m.Modify(9, 77), "")
	if cp := m.Checkpoint(); cp != 77 {
		t.Fatalf("dirty checkpoint = %d", cp)
	}
	mustOK(t, m.SetFlushed(77), "")
	_, err := m.FlushStart(9)
	mustOK(t, err, "")
	mustOK(t, m.FlushDone(9), "")
	if cp := m.Checkpoint(); cp != 78 {
		t.Fatalf("empty checkpoint = %d, want 78", cp)
	}
	rejectIs(t, m.Modify(9, 50), ReasonLSNTooSmall, "")
	if cp := m.Checkpoint(); cp != 78 {
		t.Fatalf("checkpoint after rejected modify = %d", cp)
	}
	assertInvariants(t, m)
}

// Plan：拉入 oldest 不小于 target 的前置页；多前置按页号升序；
// 跳过在途页；已入计划页不重复；Plan 不改状态。
func TestPlanDetails(t *testing.T) {
	m, _ := New(20)
	for _, spec := range [][2]int64{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	} {
		mustOK(t, m.Modify(int(spec[0]), spec[1]), "")
	}
	mustOK(t, m.AddDep(4, 2), "")
	mustOK(t, m.AddDep(5, 2), "")
	mustOK(t, m.AddDep(3, 2), "")
	plan, err := m.Plan(25)
	mustOK(t, err, "")
	// 链表次序 1,2,...：先处理页 1（无前置，直接入计划），
	// 处理页 2 时再按页号升序拉入前置 3,4,5。
	if !eqInts(plan, []int{1, 3, 4, 5, 2}) {
		t.Fatalf("plan = %v", plan)
	}
	mustOK(t, m.SetFlushed(50), "")
	_, err = m.FlushStart(1)
	mustOK(t, err, "")
	plan, err = m.Plan(60)
	mustOK(t, err, "")
	if !eqInts(plan, []int{3, 4, 5, 2}) {
		t.Fatalf("plan with in-flight = %v", plan)
	}
	if !m.pages[1].inFlight || m.DirtyCount() != 5 {
		t.Fatalf("Plan mutated state")
	}
	_, err = m.Plan(0)
	rejectIs(t, err, ReasonInvalidArg, "target 0")
	assertInvariants(t, m)
}

// 脏页已满：干净页的 Modify 被拒，已脏页仍可继续修改。
func TestDirtyFullOnlyBlocksClean(t *testing.T) {
	m, _ := New(2)
	mustOK(t, m.Modify(1, 10), "")
	mustOK(t, m.Modify(2, 20), "")
	mustOK(t, m.Modify(1, 30), "dirty page still mutable")
	rejectIs(t, m.Modify(3, 40), ReasonDirtyFull, "clean page blocked")
	mustOK(t, m.Modify(2, 40), "lsn 40 reusable after rejection")
	mustOK(t, m.SetFlushed(40), "")
	_, err := m.FlushStart(1)
	mustOK(t, err, "")
	mustOK(t, m.FlushDone(1), "")
	mustOK(t, m.Modify(3, 50), "slot freed")
	assertInvariants(t, m)
}

// 参数校验优先于一切；Modify 的 LSN 不够大优先于容量满。
func TestValidationOrder(t *testing.T) {
	_, err := New(0)
	rejectIs(t, err, ReasonInvalidArg, "bad D")
	m, _ := New(1)
	_, err = m.FlushStart(-1)
	rejectIs(t, err, ReasonInvalidArg, "bad page")
	rejectIs(t, m.Modify(0, 0), ReasonInvalidArg, "bad lsn")
	mustOK(t, m.Modify(0, 10), "")
	rejectIs(t, m.Modify(0, 10), ReasonLSNTooSmall, "lsn first")
	rejectIs(t, m.Modify(1, 10), ReasonLSNTooSmall, "lsn checked before capacity")
	rejectIs(t, m.Modify(1, 11), ReasonDirtyFull, "then full")
	mustOK(t, m.Modify(0, 11), "11 accepted after capacity rejection")
	rejectIs(t, m.AddDep(2, 2), ReasonInvalidArg, "a==b")
}

// FlushDone 页不在途拒绝。
func TestFlushDoneNotInFlight(t *testing.T) {
	m, _ := New(10)
	rejectIs(t, m.FlushDone(1), ReasonNotInFlight, "never started")
	mustOK(t, m.Modify(1, 10), "")
	rejectIs(t, m.FlushDone(1), ReasonNotInFlight, "dirty but not in flight")
}

// 并发冒烟：所有操作并发执行，最终不变量成立，且竞态检测器可运行。
func TestConcurrentSmoke(t *testing.T) {
	m, _ := New(64)
	var wg sync.WaitGroup
	var lsnMu sync.Mutex
	var lsnCounter int64
	nextLSN := func() int64 {
		lsnMu.Lock()
		lsnCounter++
		v := lsnCounter
		lsnMu.Unlock()
		return v
	}
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := g*8 + int(nextLSN()%8)
				lsn := nextLSN() * 2
				_ = m.Modify(p, lsn)
				_ = m.SetFlushed(lsn)
				_, _ = m.FlushStart(p)
				_ = m.FlushDone(p)
				_ = m.AddDep(p, (p+1)%64)
				_ = m.Checkpoint()
				if _, err := m.Plan(lsn); err != nil {
					t.Errorf("plan: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := m.CheckInvariants(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	// 让并发压力跑一会儿。
	for i := 0; i < 200; i++ {
		_ = nextLSN()
	}
	close(stop)
	wg.Wait()
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 在途期间无修改：FlushDone 后页变干净、离开链表、删除出边。
func TestFlushDoneCleanRemovesOutEdges(t *testing.T) {
	m, _ := New(10)
	mustOK(t, m.Modify(1, 10), "")
	mustOK(t, m.Modify(2, 20), "")
	mustOK(t, m.AddDep(1, 2), "")
	mustOK(t, m.SetFlushed(20), "")
	_, err := m.FlushStart(1)
	mustOK(t, err, "")
	mustOK(t, m.FlushDone(1), "")
	if pg, ok := m.Snapshot(1); ok && pg.Dirty {
		t.Fatalf("page1 should be clean: %+v", pg)
	}
	if edges := m.OutEdges(1); edges != nil {
		t.Fatalf("out edges should be gone: %v", edges)
	}
	if got := m.Order(); !eqInts(got, []int{2}) {
		t.Fatalf("order = %v", got)
	}
	assertInvariants(t, m)
}

// 在途期间被修改：FlushDone 后仍脏，并按 firstAfter 插回链表中间。
func TestFlushDoneReinsertsByOldest(t *testing.T) {
	m, _ := New(10)
	mustOK(t, m.Modify(1, 10), "")
	mustOK(t, m.Modify(2, 20), "")
	mustOK(t, m.SetFlushed(20), "")
	_, err := m.FlushStart(1)
	mustOK(t, err, "")
	mustOK(t, m.Modify(3, 30), "")
	mustOK(t, m.Modify(1, 40), "")
	mustOK(t, m.FlushDone(1), "")
	entries := m.OrderEntries()
	want := []OrderEntry{
		{Oldest: 20, Page: 2},
		{Oldest: 30, Page: 3},
		{Oldest: 40, Page: 1},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v", entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("pos %d = %+v, want %+v (full %+v)", i, entries[i], want[i], entries)
		}
	}
	pg, _ := m.Snapshot(1)
	if !pg.Dirty || pg.InFlight || pg.Oldest != 40 || pg.FirstAfter != 0 {
		t.Fatalf("page1 = %+v", pg)
	}
	assertInvariants(t, m)
}
