package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logCase 在测试日志中打印输入、结果与判定依据，便于人工复核。
func logCase(t *testing.T, name, input, result, basis string) {
	t.Helper()
	t.Logf("[%s] input=%s | result=%s | basis=%s", name, input, result, basis)
}

func newTestManager(t *testing.T, threshold float64, maxWindows int) *Manager {
	t.Helper()
	m, err := NewManager(threshold, maxWindows)
	if err != nil {
		t.Fatalf("NewManager unexpected error: %v", err)
	}
	return m
}

// TestNewManagerInvalidConfig 构造参数非法时必须拒绝。
func TestNewManagerInvalidConfig(t *testing.T) {
	if _, err := NewManager(0, 10); err == nil {
		t.Fatal("threshold=0 should be rejected")
	}
	if _, err := NewManager(-1, 10); err == nil {
		t.Fatal("negative threshold should be rejected")
	}
	if _, err := NewManager(10, 0); err == nil {
		t.Fatal("maxWindows=0 should be rejected")
	}
	logCase(t, "invalid-config", "threshold<=0 或 maxWindows<=0",
		"constructor error", "threshold 必须为正数且 maxWindows 必须大于 0")
}

// TestThresholdTriggers 首次事件隐式创建；累计值从下往上越过阈值时触发，
// 一条事件连续越过多个阈值则触发多次，Seq 为窗口内触发序号。
func TestThresholdTriggers(t *testing.T) {
	m := newTestManager(t, 10, 8)

	r1, err := m.Event("w1", 4)
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if len(r1.Triggers) != 0 {
		t.Fatalf("value 4/10 must not trigger, got %d", len(r1.Triggers))
	}
	logCase(t, "threshold", `Event("w1",4)`,
		fmt.Sprintf("total=%.0f triggers=%d", r1.Total, len(r1.Triggers)),
		"4 < 10，未越过阈值；窗口在首次事件到达时隐式创建")

	r2, err := m.Event("w1", 7)
	if err != nil {
		t.Fatalf("event 7: %v", err)
	}
	if len(r2.Triggers) != 1 || r2.Triggers[0].Seq != 1 {
		t.Fatalf("4->11 must fire once with seq 1, got %+v", r2.Triggers)
	}
	logCase(t, "threshold", `Event("w1",7)`,
		fmt.Sprintf("total=%.0f triggers=%d seq=%d", r2.Total, len(r2.Triggers), r2.Triggers[0].Seq),
		"floor(11/10)-floor(4/10)=1，从下往上越过 10 一次")

	r3, err := m.Event("w1", 25)
	if err != nil {
		t.Fatalf("event 25: %v", err)
	}
	// 11 -> 36：越过 20、30，floor(36/10)-floor(11/10)=3-1=2。
	if len(r3.Triggers) != 2 {
		t.Fatalf("want 2 crossings, got %d", len(r3.Triggers))
	}
	if r3.Triggers[0].Seq != 2 || r3.Triggers[1].Seq != 3 {
		t.Fatalf("seq must continue 2,3: %+v", r3.Triggers)
	}
	logCase(t, "threshold", `Event("w1",25)`,
		fmt.Sprintf("total=%.0f triggers=%d seqs=%d,%d", r3.Total, len(r3.Triggers),
			r3.Triggers[0].Seq, r3.Triggers[1].Seq),
		"floor(36/10)-floor(11/10)=2，连续越过 20、30 两个阈值")

	snap, _ := m.Snapshot("w1")
	if snap.FireCount != 3 || snap.Total != 36 {
		t.Fatalf("snapshot want total=36 fireCount=3, got %+v", snap)
	}
	logCase(t, "threshold", `Snapshot("w1")`,
		fmt.Sprintf("total=%.0f fireCount=%d", snap.Total, snap.FireCount),
		"快照中的累计值与触发次数为最终一致状态")
}

// TestCleanupFreeze 清理后累计值冻结保留：普通事件不再累加、不再触发。
func TestCleanupFreeze(t *testing.T) {
	m := newTestManager(t, 10, 8)

	if _, err := m.Event("w", 15); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("w"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	snap, _ := m.Snapshot("w")
	if snap.State != StateCleaned || snap.Total != 15 || snap.FireCount != 1 {
		t.Fatalf("cleaned snapshot want total=15 fire=1, got %+v", snap)
	}
	logCase(t, "cleanup-freeze", `Event 15; Cleanup`,
		fmt.Sprintf("state=%s total=%.0f fire=%d", snap.State, snap.Total, snap.FireCount),
		"清理只改状态，累计值与触发次数冻结保留")

	r, err := m.Event("w", 100)
	if err != nil {
		t.Fatalf("event on cleaned window: %v", err)
	}
	if r.Total != 15 || len(r.Triggers) != 0 {
		t.Fatalf("cleaned window must freeze, got total=%.0f triggers=%d", r.Total, len(r.Triggers))
	}
	snap, _ = m.Snapshot("w")
	if snap.Total != 15 || snap.FireCount != 1 || snap.State != StateCleaned {
		t.Fatalf("state after frozen event: %+v", snap)
	}
	logCase(t, "cleanup-freeze", `Event("w",100) on cleaned`,
		fmt.Sprintf("state=%s total=%.0f triggers=%d", r.State, r.Total, len(r.Triggers)),
		"已清理窗口不参与累加与触发，100 被丢弃")
}

// TestReviveSingleEvent 迟到事件复活：只取这一条事件的值、丢弃冻结历史、
// 状态变为 revived 且不触发。
func TestReviveSingleEvent(t *testing.T) {
	m := newTestManager(t, 10, 8)

	if _, err := m.Event("w", 99); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("w"); err != nil {
		t.Fatal(err)
	}

	r, err := m.LateEvent("w", 7)
	if err != nil {
		t.Fatalf("late event: %v", err)
	}
	if !r.Revived || r.Total != 7 || r.State != StateRevived || len(r.Triggers) != 0 {
		t.Fatalf("revive must take only the single value 7 without firing: %+v", r)
	}
	snap, _ := m.Snapshot("w")
	if snap.Total != 7 || snap.State != StateRevived || snap.FireCount != 0 {
		t.Fatalf("frozen history must be discarded: %+v", snap)
	}
	logCase(t, "revive-single", `Event 99; Cleanup; LateEvent 7`,
		fmt.Sprintf("revived=%v total=%.0f fire=%d state=%s",
			r.Revived, snap.Total, snap.FireCount, snap.State),
		"复活丢弃冻结的 99，累计值直接取本条的 7，不触发")
}

// TestRevivedSuppressesTriggers 复活后任何事件照常累加但永不触发。
func TestRevivedSuppressesTriggers(t *testing.T) {
	m := newTestManager(t, 10, 8)

	if _, err := m.Event("w", 15); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("w"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LateEvent("w", 15); err != nil {
		t.Fatal(err)
	}

	r, err := m.Event("w", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Triggers) != 0 || r.Total != 115 {
		t.Fatalf("revived window accumulates without firing: total=%.0f triggers=%d",
			r.Total, len(r.Triggers))
	}
	r2, err := m.LateEvent("w", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Triggers) != 0 || r2.Total != 215 {
		t.Fatalf("late events after revive also suppressed: total=%.0f triggers=%d",
			r2.Total, len(r2.Triggers))
	}
	snap, _ := m.Snapshot("w")
	if snap.FireCount != 0 || snap.Total != 215 {
		t.Fatalf("want total=215 fire=0, got %+v", snap)
	}
	logCase(t, "revive-suppress", `revive@15; Event 100; LateEvent 100`,
		fmt.Sprintf("total=%.0f fire=%d triggers(late)=%d",
			snap.Total, snap.FireCount, len(r2.Triggers)),
		"revived 状态照常累加 15+100+100=215，但触发被永久抑制")
}

// TestReclaimKeepsRevived 回收删除已清理未复活窗口；已复活窗口保留；活跃窗口不可回收。
func TestReclaimKeepsRevived(t *testing.T) {
	m := newTestManager(t, 10, 8)

	if _, err := m.Event("cleaned", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Event("revived", 5); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("cleaned"); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("revived"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LateEvent("revived", 3); err != nil {
		t.Fatal(err)
	}

	deleted, err := m.Reclaim("cleaned")
	if err != nil || !deleted {
		t.Fatalf("cleaned-not-revived must be reclaimed: deleted=%v err=%v", deleted, err)
	}
	if _, ok := m.Snapshot("cleaned"); ok {
		t.Fatal("reclaimed window must be gone")
	}
	logCase(t, "reclaim", `Reclaim("cleaned")`,
		"deleted=true, snapshot missing",
		"已清理且未复活 => 删除")

	kept, err := m.Reclaim("revived")
	if err != nil || kept {
		t.Fatalf("revived window must be kept: deleted=%v err=%v", kept, err)
	}
	snap, ok := m.Snapshot("revived")
	if !ok || snap.State != StateRevived || snap.Total != 3 {
		t.Fatalf("revived window survives reclaim: %+v ok=%v", snap, ok)
	}
	logCase(t, "reclaim", `Reclaim("revived")`,
		fmt.Sprintf("deleted=false, state=%s total=%.0f", snap.State, snap.Total),
		"已复活窗口保留，不回收")

	if _, err := m.Event("active", 1); err != nil {
		t.Fatal(err)
	}
	if deleted, err := m.Reclaim("active"); err != nil || deleted {
		t.Fatalf("active window must not be reclaimed: deleted=%v err=%v", deleted, err)
	}
	logCase(t, "reclaim", `Reclaim("active")`,
		"deleted=false", "活跃窗口不允许回收")
}

// TestRejectedInputsKeepState 四类非法输入必须返回互不相同的哨兵错误，
// 且失败不改变任何状态；被拒后管理器仍可正常使用。
func TestRejectedInputsKeepState(t *testing.T) {
	m := newTestManager(t, 10, 2)
	if _, err := m.Event("w", 12); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("w"); err != nil {
		t.Fatal(err)
	}

	sentinels := []error{ErrInvalidID, ErrInvalidValue, ErrWindowNotFound, ErrTooManyWindows}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) || sentinels[i].Error() == sentinels[j].Error() {
				t.Fatalf("sentinel errors must be distinct: %v vs %v", sentinels[i], sentinels[j])
			}
		}
	}

	// 先构造稳定的窗口集合：w(cleaned) 与 tmp 被回收、a(active)，随后再统一执行拒绝路径。
	if _, err := m.Event("tmp", 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup("tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reclaim("tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Event("a", 1); err != nil {
		t.Fatal(err)
	}
	before := m.SnapshotAll()

	// 1) 标识非法
	if _, err := m.Event("", 1); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("empty id: want ErrInvalidID, got %v", err)
	}
	if _, err := m.Event("   ", 1); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("blank id: want ErrInvalidID, got %v", err)
	}
	if err := m.Cleanup("\t"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("cleanup blank id: %v", err)
	}
	if _, err := m.Reclaim(""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("reclaim empty id: %v", err)
	}
	logCase(t, "reject", `Event("") / Event("   ") / Cleanup("\t") / Reclaim("")`,
		fmt.Sprintf("ErrInvalidID=%v", ErrInvalidID),
		"空串或纯空白标识非法；先校验输入，再触碰任何状态")

	// 2) 值非法（负数）
	if _, err := m.Event("w", -3); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("negative value: want ErrInvalidValue, got %v", err)
	}
	if _, err := m.LateEvent("w", -3); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("negative late value: %v", err)
	}
	logCase(t, "reject", `Event/LateEvent value=-3`,
		fmt.Sprintf("ErrInvalidValue=%v", ErrInvalidValue),
		"负值非法（普通事件与迟到事件一致），拒绝后状态不变")

	// 3) 对从未创建过的窗口发迟到事件；已回收窗口同样不可复活
	if _, err := m.LateEvent("ghost", 1); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("late event to unknown window: want ErrWindowNotFound, got %v", err)
	}
	if _, err := m.LateEvent("tmp", 1); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("late event after reclaim: %v", err)
	}
	logCase(t, "reject", `LateEvent("ghost") / LateEvent(reclaimed "tmp")`,
		fmt.Sprintf("ErrWindowNotFound=%v", ErrWindowNotFound),
		"迟到事件只能复活已存在且处于 cleaned 的窗口；不存在即拒绝")

	// 4) 窗口数超上限（当前窗口为 w、a，已达上限 2）
	if _, err := m.Event("b", 1); !errors.Is(err, ErrTooManyWindows) {
		t.Fatalf("over limit create: want ErrTooManyWindows, got %v", err)
	}
	logCase(t, "reject", `Event("b") with maxWindows=2 and windows={w,a}`,
		fmt.Sprintf("ErrTooManyWindows=%v", ErrTooManyWindows),
		"新窗口创建前在上限校验处拒绝，不写入 map")

	// 失败不改变任何状态：与拒绝前快照逐一比对。
	after := m.SnapshotAll()
	if len(after) != len(before) {
		t.Fatalf("rejected calls changed window count: before=%d after=%d", len(before), len(after))
	}
	beforeMap := map[string]WindowSnapshot{}
	for _, s := range before {
		beforeMap[s.ID] = s
	}
	for _, s := range after {
		b, ok := beforeMap[s.ID]
		if !ok || b != s {
			t.Fatalf("rejected calls changed window %q: before=%+v after=%+v", s.ID, b, s)
		}
	}

	// 被拒后仍可继续正常使用。
	r, err := m.Event("w", 5)
	if err != nil {
		t.Fatalf("manager must remain usable after rejection: %v", err)
	}
	if r.Total != 12 || len(r.Triggers) != 0 {
		t.Fatalf("cleaned w still frozen: %+v", r)
	}
	r, err = m.Event("a", 9)
	if err != nil || len(r.Triggers) != 1 {
		t.Fatalf("normal usage must still work: %+v err=%v", r, err)
	}
	logCase(t, "reject", `post-reject Event("w",5); Event("a",9)`,
		fmt.Sprintf("w.total=%.0f frozen; a triggers=%d", 12.0, len(r.Triggers)),
		"拒绝是纯失败路径，管理器与各窗口保持可用且一致")
}

// TestConcurrentAccumulation 并发投递事件：同一窗口累计值与触发次数必须精确正确，
// 并发读快照与 TotalFireCount 不得读到撕裂值。
func TestConcurrentAccumulation(t *testing.T) {
	const (
		goroutines = 16
		perG       = 200
		threshold  = 10.0
	)
	m := newTestManager(t, threshold, 8)

	var writers sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < perG; i++ {
				if _, err := m.Event("c", 1); err != nil {
					t.Errorf("concurrent event: %v", err)
					return
				}
			}
		}()
	}

	var readers sync.WaitGroup
	stop := make(chan struct{})
	readers.Add(2)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = m.Snapshot("c")
			}
		}
	}()
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = m.TotalFireCount()
			}
		}
	}()

	writers.Wait()
	close(stop)
	readers.Wait()

	wantTotal := float64(goroutines * perG)
	snap, ok := m.Snapshot("c")
	if !ok {
		t.Fatal("window c missing")
	}
	if snap.Total != wantTotal {
		t.Fatalf("total want %.0f got %.0f", wantTotal, snap.Total)
	}
	wantFires := int(wantTotal / threshold)
	if snap.FireCount != wantFires {
		t.Fatalf("fireCount want %d got %d", wantFires, snap.FireCount)
	}
	if m.TotalFireCount() != wantFires {
		t.Fatalf("TotalFireCount want %d got %d", wantFires, m.TotalFireCount())
	}
	logCase(t, "concurrent-accumulate",
		fmt.Sprintf("%d goroutines x %d events of value 1, threshold=%.0f", goroutines, perG, threshold),
		fmt.Sprintf("total=%.0f fireCount=%d totalFireCount=%d",
			snap.Total, snap.FireCount, m.TotalFireCount()),
		"窗口互斥锁串行化累加；total=3200 且 fireCount=floor(3200/10)=320")
}

// TestConcurrentReclaimVsRevive 回收与迟到并发：每个已清理未复活窗口最终
// 要么被回收（不存在），要么被复活（revived 且累计值等于复活值），二者互斥且穷尽。
func TestConcurrentReclaimVsRevive(t *testing.T) {
	const windowsN = 200
	m := newTestManager(t, 10, windowsN)

	for i := 0; i < windowsN; i++ {
		id := fmt.Sprintf("win-%03d", i)
		if _, err := m.Event(id, 99); err != nil {
			t.Fatal(err)
		}
		if err := m.Cleanup(id); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	reclaimWins := make(chan string, windowsN)
	reviveWins := make(chan string, windowsN)

	for i := 0; i < windowsN; i++ {
		id := fmt.Sprintf("win-%03d", i)
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			deleted, err := m.Reclaim(id)
			if err != nil {
				t.Errorf("reclaim %s: %v", id, err)
				return
			}
			if deleted {
				reclaimWins <- id
			}
		}(id)
		go func(id string) {
			defer wg.Done()
			r, err := m.LateEvent(id, 3)
			if err != nil {
				// 回收先赢得互斥时，迟到事件只能收到 ErrWindowNotFound，这是合法结局。
				if !errors.Is(err, ErrWindowNotFound) {
					t.Errorf("late %s: unexpected err %v", id, err)
				}
				return
			}
			if r.Revived {
				reviveWins <- id
			}
		}(id)
	}
	wg.Wait()
	close(reclaimWins)
	close(reviveWins)

	reclaimSet := map[string]bool{}
	for id := range reclaimWins {
		if reclaimSet[id] {
			t.Fatalf("window %s reclaimed twice", id)
		}
		reclaimSet[id] = true
	}
	reviveSet := map[string]bool{}
	for id := range reviveWins {
		if reviveSet[id] {
			t.Fatalf("window %s revived twice", id)
		}
		reviveSet[id] = true
	}

	// 互斥：没有任何窗口同时报告“被回收”和“被复活”。
	for id := range reclaimSet {
		if reviveSet[id] {
			t.Fatalf("mutex violated: %s both reclaimed and revived", id)
		}
	}

	// 完备划分：最终每个窗口要么不存在（被回收），要么为 revived（被复活）。
	finalReclaimed, finalRevived := 0, 0
	for i := 0; i < windowsN; i++ {
		id := fmt.Sprintf("win-%03d", i)
		snap, ok := m.Snapshot(id)
		if !ok {
			finalReclaimed++
			if !reclaimSet[id] {
				t.Fatalf("missing %s but no reclaim reported it", id)
			}
			continue
		}
		finalRevived++
		if !reviveSet[id] {
			t.Fatalf("present %s but no revive reported it", id)
		}
		if snap.State != StateRevived || snap.Total != 3 || snap.FireCount != 0 {
			t.Fatalf("revived %s inconsistent: %+v", id, snap)
		}
	}

	if len(reclaimSet)+len(reviveSet) != windowsN {
		t.Fatalf("partition size want %d, got reclaim=%d revive=%d",
			windowsN, len(reclaimSet), len(reviveSet))
	}
	if finalReclaimed != len(reclaimSet) || finalRevived != len(reviveSet) {
		t.Fatalf("final state mismatch: reclaimed reported=%d final=%d, revived reported=%d final=%d",
			len(reclaimSet), finalReclaimed, len(reviveSet), finalRevived)
	}

	logCase(t, "concurrent-reclaim-revive",
		fmt.Sprintf("%d cleaned windows, each raced by 1 reclaim + 1 late event", windowsN),
		fmt.Sprintf("reclaimed=%d revived=%d total=%d",
			len(reclaimSet), len(reviveSet), len(reclaimSet)+len(reviveSet)),
		"Manager 写锁临界区内完成 判定+删除 / 判定+复活，互斥且构成完备划分")
}

// TestConcurrentMixedOperations 事件、清理、迟到、回收、快照与 TotalFireCount 混合并发，
// 跑完后所有现存窗口必须处于三种合法状态之一且字段自洽。
func TestConcurrentMixedOperations(t *testing.T) {
	const idsN = 40
	m := newTestManager(t, 5, idsN)

	var wg sync.WaitGroup
	for i := 0; i < idsN; i++ {
		id := fmt.Sprintf("m-%02d", i)
		if _, err := m.Event(id, 1); err != nil {
			t.Fatal(err)
		}

		wg.Add(4)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				_, _ = m.Event(id, 1)
			}
		}()
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				_ = m.Cleanup(id)
			}
		}()
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				_, _ = m.LateEvent(id, 2)
			}
		}()
		go func() {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				_, _ = m.Reclaim(id)
			}
		}()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		prev := 0
		for k := 0; k < 500; k++ {
			v := m.LifetimeFireCount()
			if v < prev {
				t.Errorf("LifetimeFireCount must be monotonic: %d -> %d", prev, v)
				return
			}
			prev = v
		}
	}()
	go func() {
		defer wg.Done()
		prev := -1
		for k := 0; k < 500; k++ {
			v := m.TotalFireCount()
			if prev >= 0 && v > prev {
				// 现存窗口触发数之和只会因复活清零/回收而下降或持平，不应自增以外突变；
				// 这里仅保证读取始终安全。
			}
			prev = v
		}
	}()

	wg.Wait()

	sum := 0
	for _, s := range m.SnapshotAll() {
		switch s.State {
		case StateActive, StateCleaned, StateRevived:
		default:
			t.Fatalf("window %s has illegal state %q", s.ID, s.State)
		}
		if s.State == StateRevived && s.FireCount != 0 {
			t.Fatalf("revived window %s must have fireCount 0, got %d", s.ID, s.FireCount)
		}
		sum += s.FireCount
	}
	if sum != m.TotalFireCount() {
		t.Fatalf("sum of snapshot fire counts %d != TotalFireCount %d", sum, m.TotalFireCount())
	}
	logCase(t, "concurrent-mixed",
		fmt.Sprintf("%d windows x {100 events, 10 cleanups, 10 late, 10 reclaims} + readers", idsN),
		fmt.Sprintf("survivors=%d totalFireCount=%d, all states legal", len(m.SnapshotAll()), sum),
		"混合并发后无非法状态；revived 窗口零触发；触发数总和与全局读数一致")
}
