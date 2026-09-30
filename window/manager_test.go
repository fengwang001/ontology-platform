package window

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustManager(t *testing.T, threshold int64, maxWindows int) *Manager {
	t.Helper()
	m, err := NewManager(threshold, maxWindows)
	if err != nil {
		t.Fatalf("NewManager(%d, %d) 出错: %v", threshold, maxWindows, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Manager, id string, value int64) []TriggerEvent {
	t.Helper()
	evs, err := m.AddEvent(id, value)
	if err != nil {
		t.Fatalf("AddEvent(%q, %d) 出错: %v", id, value, err)
	}
	return evs
}

func mustSnapshot(t *testing.T, m *Manager, id string) Snapshot {
	t.Helper()
	snap, ok := m.Snapshot(id)
	if !ok {
		t.Fatalf("Snapshot(%q) 不存在", id)
	}
	return snap
}

// 阈值触发：累计值从下往上越过阈值倍数时逐次触发，单条事件越过多倍则多次触发。
func TestThresholdTrigger(t *testing.T) {
	m := mustManager(t, 10, 8)

	evs := mustAdd(t, m, "w1", 9)
	t.Logf("输入 AddEvent(w1, 9)，阈值=10；结果触发 %d 条；判定依据：累计 9 未越过 10，应为 0", len(evs))
	if len(evs) != 0 {
		t.Fatalf("不应触发, 实际 %d 条", len(evs))
	}

	evs = mustAdd(t, m, "w1", 2)
	t.Logf("输入 AddEvent(w1, 2)，累计 9+2=11；结果触发 %d 条；判定依据：11 越过 1 个 10 的倍数，应为 1", len(evs))
	if len(evs) != 1 || evs[0].Count != 1 || evs[0].Total != 11 || evs[0].WindowID != "w1" {
		t.Fatalf("触发事件不符: %+v", evs)
	}

	evs = mustAdd(t, m, "w1", 25)
	t.Logf("输入 AddEvent(w1, 25)，累计 11+25=36；结果触发 %d 条；判定依据：从 11 到 36 连续越过 20、30 两个倍数，应为 2", len(evs))
	if len(evs) != 2 || evs[0].Count != 2 || evs[1].Count != 3 {
		t.Fatalf("连续越界触发不符: %+v", evs)
	}

	snap := mustSnapshot(t, m, "w1")
	t.Logf("快照: %+v；判定依据：累计值 36、触发 3 次、状态 Active", snap)
	if snap.Total != 36 || snap.Triggers != 3 || snap.State != StateActive {
		t.Fatalf("快照不符: %+v", snap)
	}
	if got := m.TotalTriggers(); got != 3 {
		t.Fatalf("TotalTriggers = %d, 期望 3", got)
	}
}

// 清理冻结：清理后累计值保留但不再参与，普通事件被拒且状态不变。
func TestCleanupFreeze(t *testing.T) {
	m := mustManager(t, 10, 8)
	mustAdd(t, m, "w1", 15)

	if err := m.Cleanup("w1"); err != nil {
		t.Fatalf("Cleanup(w1) 出错: %v", err)
	}
	snap := mustSnapshot(t, m, "w1")
	t.Logf("清理后快照: %+v；判定依据：累计值 15 冻结保留、状态 Cleaned", snap)
	if snap.Total != 15 || snap.Triggers != 1 || snap.State != StateCleaned {
		t.Fatalf("清理后快照不符: %+v", snap)
	}

	_, err := m.AddEvent("w1", 5)
	t.Logf("输入 AddEvent(w1, 5)（已清理）；结果 err=%v；判定依据：冻结窗口不再参与累计，应拒绝为 ErrWindowCleaned", err)
	if !errors.Is(err, ErrWindowCleaned) {
		t.Fatalf("期望 ErrWindowCleaned, 实际 %v", err)
	}
	snap = mustSnapshot(t, m, "w1")
	if snap.Total != 15 || snap.Triggers != 1 || snap.State != StateCleaned {
		t.Fatalf("被拒后状态被改变: %+v", snap)
	}
	t.Logf("被拒后快照: %+v；判定依据：失败不改变任何状态，仍应为 total=15 triggers=1 Cleaned", snap)
}

// 复活只取一条：迟到事件复活已清理窗口，累计值直接取该事件值，丢弃冻结历史，且不触发。
func TestReviveTakesOnlyLateEvent(t *testing.T) {
	m := mustManager(t, 10, 8)
	mustAdd(t, m, "w1", 15) // 冻结历史：total=15, triggers=1
	if err := m.Cleanup("w1"); err != nil {
		t.Fatalf("Cleanup(w1) 出错: %v", err)
	}

	evs, err := m.LateEvent("w1", 7)
	t.Logf("输入 LateEvent(w1, 7)（已清理）；结果触发 %d 条, err=%v；判定依据：复活只服务这一条事件，不触发", len(evs), err)
	if err != nil || len(evs) != 0 {
		t.Fatalf("复活不应触发且不应报错: evs=%v err=%v", evs, err)
	}
	snap := mustSnapshot(t, m, "w1")
	t.Logf("复活后快照: %+v；判定依据：累计值取本条事件值 7（丢弃冻结的 15），状态 Revived，触发次数保留为 1", snap)
	if snap.Total != 7 || snap.State != StateRevived || snap.Triggers != 1 {
		t.Fatalf("复活后快照不符: %+v", snap)
	}
}

// 复活后抑制触发：已复活窗口照常再累加但永不触发。
func TestRevivedSuppressesTrigger(t *testing.T) {
	m := mustManager(t, 10, 8)
	mustAdd(t, m, "w1", 15)
	if err := m.Cleanup("w1"); err != nil {
		t.Fatalf("Cleanup(w1) 出错: %v", err)
	}
	if _, err := m.LateEvent("w1", 7); err != nil {
		t.Fatalf("LateEvent(w1, 7) 出错: %v", err)
	}

	evs := mustAdd(t, m, "w1", 50)
	t.Logf("输入 AddEvent(w1, 50)（已复活），累计 7+50=57；结果触发 %d 条；判定依据：已复活窗口不再触发，应为 0", len(evs))
	if len(evs) != 0 {
		t.Fatalf("已复活窗口不应触发: %+v", evs)
	}
	lateEvs, err := m.LateEvent("w1", 100)
	t.Logf("输入 LateEvent(w1, 100)（已复活），累计 57+100=157；结果触发 %d 条；判定依据：已复活窗口任何事件都只累加不触发", len(lateEvs))
	if err != nil || len(lateEvs) != 0 {
		t.Fatalf("已复活窗口迟到事件不应触发: evs=%v err=%v", lateEvs, err)
	}
	snap := mustSnapshot(t, m, "w1")
	t.Logf("快照: %+v；判定依据：total=157、triggers 保持 1、状态 Revived", snap)
	if snap.Total != 157 || snap.Triggers != 1 || snap.State != StateRevived {
		t.Fatalf("快照不符: %+v", snap)
	}
}

// 回收保留已复活窗口：已清理未复活的被删除，已复活的保留。
func TestReapKeepsRevived(t *testing.T) {
	m := mustManager(t, 10, 8)
	mustAdd(t, m, "cleaned", 5)
	mustAdd(t, m, "revived", 5)
	mustAdd(t, m, "active", 5)
	for _, id := range []string{"cleaned", "revived"} {
		if err := m.Cleanup(id); err != nil {
			t.Fatalf("Cleanup(%s) 出错: %v", id, err)
		}
	}
	if _, err := m.LateEvent("revived", 3); err != nil {
		t.Fatalf("LateEvent(revived, 3) 出错: %v", err)
	}

	reaped := m.Reap()
	t.Logf("回收结果: %v；判定依据：仅已清理未复活的 cleaned 被回收，revived/active 保留", reaped)
	if len(reaped) != 1 || reaped[0] != "cleaned" {
		t.Fatalf("回收列表不符: %v", reaped)
	}
	if _, ok := m.Snapshot("cleaned"); ok {
		t.Fatal("cleaned 应已被回收删除")
	}
	snap := mustSnapshot(t, m, "revived")
	if snap.State != StateRevived || snap.Total != 3 {
		t.Fatalf("已复活窗口应保留: %+v", snap)
	}
	if m.Len() != 2 {
		t.Fatalf("Len = %d, 期望 2", m.Len())
	}
	t.Logf("回收后窗口数=%d；判定依据：revived 与 active 两个窗口保留", m.Len())
}

// 四类非法输入：错误互不相同、可判定，且被拒后状态不变、管理器仍可正常使用。
func TestInvalidInputsRejected(t *testing.T) {
	m := mustManager(t, 10, 2)
	mustAdd(t, m, "w1", 5)
	before := m.Snapshots()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"非法标识", func() error { _, err := m.AddEvent("  ", 1); return err }, ErrInvalidID},
		{"非法值", func() error { _, err := m.AddEvent("w1", 0); return err }, ErrInvalidValue},
		{"迟到事件到未创建窗口", func() error { _, err := m.LateEvent("ghost", 1); return err }, ErrWindowNotFound},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		err := c.call()
		t.Logf("输入[%s]；结果 err=%v；判定依据：应为 %v", c.name, err, c.want)
		if !errors.Is(err, c.want) {
			t.Fatalf("[%s] 期望 %v, 实际 %v", c.name, c.want, err)
		}
		if seen[err] {
			t.Fatalf("错误不互不相同: %v 重复", err)
		}
		seen[err] = true
	}

	afterRejected := m.Snapshots()
	t.Logf("拒绝前快照: %+v；拒绝后快照: %+v；判定依据：失败不改变任何状态，二者应一致", before, afterRejected)
	if len(afterRejected) != 1 || afterRejected[0].Total != 5 || afterRejected[0].Triggers != 0 {
		t.Fatalf("状态被非法输入改变: %+v", afterRejected)
	}

	// 窗口数超上限：先占满第二个窗口，再创建新窗口应被拒。
	mustAdd(t, m, "w2", 1)
	_, err := m.AddEvent("w3", 1)
	t.Logf("输入 AddEvent(w3, 1)（已达上限 2）；结果 err=%v；判定依据：应为 ErrTooManyWindows", err)
	if !errors.Is(err, ErrTooManyWindows) {
		t.Fatalf("期望 ErrTooManyWindows, 实际 %v", err)
	}
	if seen[err] {
		t.Fatalf("错误不互不相同: %v 与前面重复", err)
	}

	after := m.Snapshots()
	t.Logf("超限拒绝后快照: %+v；判定依据：仍只有 w1=5、w2=1 两个窗口", after)
	if len(after) != 2 || after[0].Total != 5 || after[1].Total != 1 {
		t.Fatalf("状态被非法输入改变: %+v", after)
	}

	// 被拒后仍可正常使用。
	evs := mustAdd(t, m, "w1", 6)
	t.Logf("输入 AddEvent(w1, 6)（拒绝之后）；结果触发 %d 条；判定依据：5+6=11 越过阈值 10，应正常触发 1 次", len(evs))
	if len(evs) != 1 {
		t.Fatalf("拒绝后无法正常使用: %+v", evs)
	}
}

// 并发累计：同一窗口并发累加，累计值与触发次数必须正确。
func TestConcurrentAccumulate(t *testing.T) {
	const (
		threshold = 10
		workers   = 8
		perWorker = 500
	)
	m := mustManager(t, threshold, 16)
	var wg sync.WaitGroup
	var mu sync.Mutex
	totalEvents := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := 0
			for i := 0; i < perWorker; i++ {
				evs, err := m.AddEvent("w1", 1)
				if err != nil {
					t.Errorf("AddEvent 出错: %v", err)
					return
				}
				local += len(evs)
			}
			mu.Lock()
			totalEvents += local
			mu.Unlock()
		}()
	}
	wg.Wait()

	const total = workers * perWorker
	snap := mustSnapshot(t, m, "w1")
	wantTriggers := int64(total / threshold)
	t.Logf("并发输入 %d 条值为 1 的事件（阈值 %d）；结果 total=%d triggers=%d；判定依据：total 应等于事件数，triggers 应等于 total/threshold",
		total, threshold, snap.Total, snap.Triggers)
	if snap.Total != total || snap.Triggers != wantTriggers {
		t.Fatalf("并发累计不符: %+v, 期望 total=%d triggers=%d", snap, total, wantTriggers)
	}
	if int64(totalEvents) != wantTriggers {
		t.Fatalf("产出的触发事件总数 = %d, 期望 %d", totalEvents, wantTriggers)
	}
	if got := m.TotalTriggers(); got != wantTriggers {
		t.Fatalf("TotalTriggers = %d, 期望 %d", got, wantTriggers)
	}
}

// 回收与复活并发互斥：每个已清理未复活的窗口要么被回收、要么被复活，二者不可同时发生。
func TestConcurrentReapAndRevive(t *testing.T) {
	const windows = 64
	m := mustManager(t, 10, windows)
	ids := make([]string, windows)
	for i := range ids {
		ids[i] = fmt.Sprintf("w%03d", i)
		mustAdd(t, m, ids[i], 5)
		if err := m.Cleanup(ids[i]); err != nil {
			t.Fatalf("Cleanup(%s) 出错: %v", ids[i], err)
		}
	}

	// 并发：一半 goroutine 发迟到事件尝试复活，另一半不断回收。
	lateErrs := make([]error, windows)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, lateErrs[i] = m.LateEvent(ids[i], 7)
		}(i)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Reap()
		}()
	}
	wg.Wait()

	// 判定：复活成功 <=> 窗口存在且为 Revived 且 total=7；
	// 复活失败(ErrWindowNotFound) <=> 窗口已被回收删除。两者必居其一。
	revived, reaped := 0, 0
	for i, id := range ids {
		snap, ok := m.Snapshot(id)
		switch {
		case lateErrs[i] == nil:
			if !ok || snap.State != StateRevived || snap.Total != 7 {
				t.Fatalf("窗口 %s 复活成功但状态不一致: ok=%v snap=%+v", id, ok, snap)
			}
			revived++
		case errors.Is(lateErrs[i], ErrWindowNotFound):
			if ok {
				t.Fatalf("窗口 %s 已被回收却又存在: %+v", id, snap)
			}
			reaped++
		default:
			t.Fatalf("窗口 %s 迟到事件返回意外错误: %v", id, lateErrs[i])
		}
	}
	t.Logf("并发复活与回收跑完：复活 %d 个、回收 %d 个；判定依据：每个已清理未复活窗口二者必居其一，合计应等于 %d", revived, reaped, windows)
	if revived+reaped != windows {
		t.Fatalf("状态不一致: revived=%d reaped=%d, 合计应=%d", revived, reaped, windows)
	}
	// 跑完后不应存在任何 Cleaned 状态窗口。
	for _, snap := range m.Snapshots() {
		if snap.State == StateCleaned {
			t.Fatalf("跑完后仍存在已清理未决窗口: %+v", snap)
		}
	}
}

// 并发混合调用：事件、清理、迟到、回收与并发读同时进行，管理器保持可用且状态合法。
func TestConcurrentMixedOperations(t *testing.T) {
	m := mustManager(t, 5, 32)
	var wg sync.WaitGroup
	ops := []func(int){
		func(i int) { _, _ = m.AddEvent(fmt.Sprintf("w%02d", i%8), 3) },
		func(i int) { _ = m.Cleanup(fmt.Sprintf("w%02d", i%8)) },
		func(i int) { _, _ = m.LateEvent(fmt.Sprintf("w%02d", i%8), 2) },
		func(i int) { m.Reap() },
		func(i int) { m.Snapshots() },
		func(i int) { m.TotalTriggers() },
	}
	for op := range ops {
		wg.Add(1)
		go func(op int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ops[op](i)
			}
		}(op)
	}
	wg.Wait()

	for _, snap := range m.Snapshots() {
		t.Logf("最终快照: %+v", snap)
		if snap.Total <= 0 || snap.Triggers < 0 {
			t.Fatalf("非法最终状态: %+v", snap)
		}
		if snap.State == StateActive && snap.Triggers > snap.Total/5 {
			t.Fatalf("活跃窗口触发次数超过累计值允许的上界: %+v", snap)
		}
	}
	t.Logf("混合并发跑完，窗口数=%d，总触发=%d；判定依据：无竞态、无非法状态即通过", m.Len(), m.TotalTriggers())
}
