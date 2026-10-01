package txlog

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, f, i int64, m int) *Log {
	t.Helper()
	l, err := New(f, i, m)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) 失败: %v", f, i, m, err)
	}
	return l
}

func mustSend(t *testing.T, l *Log, txID, payload string, now int64) {
	t.Helper()
	if err := l.Send(txID, payload, now); err != nil {
		t.Fatalf("Send(%q, now=%d) 失败: %v", txID, now, err)
	}
}

func mustTick(t *testing.T, l *Log, now int64, cb CheckCallback) {
	t.Helper()
	if err := l.Tick(now, cb); err != nil {
		t.Fatalf("Tick(now=%d) 失败: %v", now, err)
	}
}

// countingCB 返回一个记录调用次数、恒返回给定决策的回调。
func countingCB(calls *int, d Decision) CheckCallback {
	return func(txID, payload string) Decision {
		*calls++
		return d
	}
}

// 位点按提交先后而非发送先后分配。
func TestCommitOrderDeterminesOffset(t *testing.T) {
	l := mustNew(t, 1000, 10, 3)
	mustSend(t, l, "A", "pa", 0)
	mustSend(t, l, "B", "pb", 1)
	mustSend(t, l, "C", "pc", 2)

	offC, err := l.Commit("C")
	if err != nil {
		t.Fatalf("Commit(C) 失败: %v", err)
	}
	offA, err := l.Commit("A")
	if err != nil {
		t.Fatalf("Commit(A) 失败: %v", err)
	}
	t.Logf("输入: 发送序 A@0,B@1,C@2；提交序 C,A")
	t.Logf("输出: C 位点=%d, A 位点=%d；判定依据: 位点在提交时刻按提交先后分配", offC, offA)

	if offC != 0 || offA != 1 {
		t.Fatalf("位点应按提交序分配: 期望 C=0,A=1, 实际 C=%d,A=%d", offC, offA)
	}
	entries := l.Entries()
	if len(entries) != 2 || entries[0].TxID != "C" || entries[1].TxID != "A" {
		t.Fatalf("日志顺序错误: %+v", entries)
	}
	if entries[0].Offset != 0 || entries[1].Offset != 1 {
		t.Fatalf("位点应从 0 起连续: %+v", entries)
	}
	recB, _ := l.Get("B")
	if recB.State != StatePending || recB.Offset != -1 {
		t.Fatalf("B 应为待定且不占位点: %+v", recB)
	}
}

// 首次回查恰好在创建后第 F 毫秒可被触发。
func TestFirstCheckExactlyAtF(t *testing.T) {
	const F = 100
	l := mustNew(t, F, 10, 3)
	mustSend(t, l, "A", "pa", 10) // 创建于 10，首次到点应为 10+F=110

	calls := 0
	mustTick(t, l, 109, countingCB(&calls, DecisionUnknown))
	t.Logf("输入: 创建=10, F=%d, Tick(109)；输出: 回查次数=%d；判定依据: 109<110 未到点", F, calls)
	if calls != 0 {
		t.Fatalf("第 F 毫秒前不应回查: calls=%d", calls)
	}

	mustTick(t, l, 110, countingCB(&calls, DecisionUnknown))
	t.Logf("输入: Tick(110)；输出: 回查次数=%d；判定依据: 110==创建+F 恰可到点", calls)
	if calls != 1 {
		t.Fatalf("第 F 毫秒应恰好回查一次: calls=%d", calls)
	}
	rec, _ := l.Get("A")
	if rec.CheckCount != 1 || rec.NextCheckAt != 110+10 {
		t.Fatalf("回查计数或下次到点错误: %+v", rec)
	}
}

// 回查间隔从实际回查时刻起算，而非从原定时刻起算。
func TestIntervalFromActualCheckTime(t *testing.T) {
	l := mustNew(t, 10, 50, 5)
	mustSend(t, l, "A", "pa", 0) // 首次到点 10

	calls := 0
	mustTick(t, l, 100, countingCB(&calls, DecisionUnknown)) // 首次回查实际发生在 100
	if calls != 1 {
		t.Fatalf("首次回查应发生: calls=%d", calls)
	}
	rec, _ := l.Get("A")
	t.Logf("输入: 创建=0, F=10, I=50, 首次实际回查=100；输出: NextCheckAt=%d；判定依据: 间隔从实际回查时刻 100 起算", rec.NextCheckAt)
	if rec.NextCheckAt != 150 {
		t.Fatalf("下次到点应为 100+50=150（非原定 10+50=60）: %d", rec.NextCheckAt)
	}

	mustTick(t, l, 149, countingCB(&calls, DecisionUnknown))
	if calls != 1 {
		t.Fatalf("149<150 不应再次回查: calls=%d", calls)
	}
	mustTick(t, l, 150, countingCB(&calls, DecisionUnknown))
	t.Logf("输入: Tick(150)；输出: 回查次数=%d；判定依据: 150==100+I 到点", calls)
	if calls != 2 {
		t.Fatalf("150 应再次回查: calls=%d", calls)
	}
}

// 一次推进跨越多个间隔仍只回查一次。
func TestTickCrossingMultipleIntervalsChecksOnce(t *testing.T) {
	l := mustNew(t, 0, 10, 5)
	mustSend(t, l, "A", "pa", 0) // 到点 0,10,20,30...

	calls := 0
	mustTick(t, l, 35, countingCB(&calls, DecisionUnknown))
	rec, _ := l.Get("A")
	t.Logf("输入: I=10, Tick(35) 跨越 3 个间隔；输出: 回查次数=%d, NextCheckAt=%d；判定依据: 每次推进每条至多回查一次, 下次从 35 起算", rec.CheckCount, rec.NextCheckAt)
	if calls != 1 || rec.CheckCount != 1 {
		t.Fatalf("跨越多间隔仍只回查一次: calls=%d count=%d", calls, rec.CheckCount)
	}
	if rec.NextCheckAt != 45 {
		t.Fatalf("下次到点应为 35+10=45: %d", rec.NextCheckAt)
	}
}

// 第 M 次回调仍返回未知时立即回滚，原因为回查耗尽。
func TestMthUnknownRollsBackExhausted(t *testing.T) {
	l := mustNew(t, 0, 1, 3)
	mustSend(t, l, "A", "pa", 0)

	calls := 0
	mustTick(t, l, 0, countingCB(&calls, DecisionUnknown)) // 第 1 次
	mustTick(t, l, 1, countingCB(&calls, DecisionUnknown)) // 第 2 次
	rec, _ := l.Get("A")
	if rec.State != StatePending || rec.CheckCount != 2 {
		t.Fatalf("前 M-1 次未知应继续等待: %+v", rec)
	}
	mustTick(t, l, 2, countingCB(&calls, DecisionUnknown)) // 第 3 次 == M
	rec, _ = l.Get("A")
	t.Logf("输入: M=3, 三次回调均返回未知；输出: 状态=%s, 原因=%q, 回查次数=%d；判定依据: 第 M 次未知即回滚, 原因为回查耗尽",
		rec.State, rec.RollbackReason, rec.CheckCount)
	if rec.State != StateRolledBack || rec.RollbackReason != ReasonCheckExhausted {
		t.Fatalf("第 M 次未知应回滚且原因为回查耗尽: %+v", rec)
	}
	if rec.CheckCount != 3 || calls != 3 {
		t.Fatalf("回查次数应为 3: count=%d calls=%d", rec.CheckCount, calls)
	}
	if len(l.Entries()) != 0 {
		t.Fatalf("回滚消息不应进入日志: %+v", l.Entries())
	}
}

// 回调内先行提交/回滚后，回调返回值被忽略。
func TestCallbackSideEffectIgnoresReturn(t *testing.T) {
	l := mustNew(t, 0, 10, 3)
	mustSend(t, l, "A", "pa", 0)
	mustSend(t, l, "B", "pb", 0)

	cb := func(txID, payload string) Decision {
		switch txID {
		case "A":
			if _, err := l.Commit("A"); err != nil { // 回调内先行提交
				t.Errorf("回调内 Commit(A) 失败: %v", err)
			}
			return DecisionRollback // 应被忽略
		default:
			if err := l.Rollback("B"); err != nil { // 回调内先行回滚
				t.Errorf("回调内 Rollback(B) 失败: %v", err)
			}
			return DecisionCommit // 应被忽略
		}
	}
	mustTick(t, l, 0, cb)

	recA, _ := l.Get("A")
	recB, _ := l.Get("B")
	t.Logf("输入: 回调内先 Commit(A) 再返回回滚, 先 Rollback(B) 再返回提交；输出: A=%s(位点 %d), B=%s(原因 %q)；判定依据: 回调返回时已进入终态则忽略返回值",
		recA.State, recA.Offset, recB.State, recB.RollbackReason)
	if recA.State != StateCommitted || recA.Offset != 0 {
		t.Fatalf("A 应保持已提交: %+v", recA)
	}
	if recB.State != StateRolledBack || recB.RollbackReason != ReasonExplicit {
		t.Fatalf("B 应保持已回滚: %+v", recB)
	}
	if recA.CheckCount != 1 || recB.CheckCount != 1 {
		t.Fatalf("回调被调用即计一次: A=%d B=%d", recA.CheckCount, recB.CheckCount)
	}
	entries := l.Entries()
	if len(entries) != 1 || entries[0].TxID != "A" {
		t.Fatalf("日志应只含 A: %+v", entries)
	}
}

// 到点集合在推进开始时确定：轮到某条时已进入终态则跳过且不计次数；
// 推进期间新发送的消息不在本次处理。
func TestTickSnapshotSkipsTerminalAndNewSends(t *testing.T) {
	l := mustNew(t, 0, 10, 5)
	mustSend(t, l, "A", "pa", 0)
	mustSend(t, l, "B", "pb", 0)

	var checked []string
	cb := func(txID, payload string) Decision {
		checked = append(checked, txID)
		if txID == "A" {
			// 轮到 B 之前先让 B 进入终态。
			if _, err := l.Commit("B"); err != nil {
				t.Errorf("回调内 Commit(B) 失败: %v", err)
			}
			// 推进期间新发送 C（F=0 立即到点），不应在本次处理。
			if err := l.Send("C", "pc", 0); err != nil {
				t.Errorf("回调内 Send(C) 失败: %v", err)
			}
		}
		return DecisionUnknown
	}
	mustTick(t, l, 0, cb)

	recB, _ := l.Get("B")
	recC, _ := l.Get("C")
	t.Logf("输入: 到点集合 [A,B], 回调内提交 B 并新发送 C；输出: 被回查=%v, B 次数=%d, C 次数=%d；判定依据: 到点集合在推进开始时确定, 终态跳过不计次",
		checked, recB.CheckCount, recC.CheckCount)
	if len(checked) != 1 || checked[0] != "A" {
		t.Fatalf("B 已进入终态应被跳过: checked=%v", checked)
	}
	if recB.CheckCount != 0 {
		t.Fatalf("跳过不应计回查次数: %+v", recB)
	}
	if recC.CheckCount != 0 {
		t.Fatalf("推进期间新发送的消息不应在本次处理: %+v", recC)
	}

	// 下一次推进 C 才到点。
	mustTick(t, l, 1, countingCB(new(int), DecisionUnknown))
	recC, _ = l.Get("C")
	if recC.CheckCount != 1 {
		t.Fatalf("C 应在下次推进被回查: %+v", recC)
	}
}

// 终态后重复操作、未知标识、重复发送四类拒绝两两可区分，
// 且被拒绝的操作不改变消息状态、日志与回查计数。
func TestRejectReasonsAreDistinct(t *testing.T) {
	l := mustNew(t, 0, 10, 3)
	mustSend(t, l, "C1", "p", 0)
	mustSend(t, l, "R1", "p", 0)
	if _, err := l.Commit("C1"); err != nil {
		t.Fatalf("Commit(C1) 失败: %v", err)
	}
	if err := l.Rollback("R1"); err != nil {
		t.Fatalf("Rollback(R1) 失败: %v", err)
	}

	all := []error{ErrUnknownTx, ErrDuplicateTx, ErrAlreadyCommitted, ErrAlreadyRolledBack}
	check := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: 期望 %v, 实际 %v", name, want, err)
		}
		for _, other := range all {
			if other != want && errors.Is(err, other) {
				t.Fatalf("%s: %v 不应可判定为 %v", name, err, other)
			}
		}
		t.Logf("输入: %s；输出: %v；判定依据: errors.Is 仅匹配 %v", name, err, want)
	}

	_, err := l.Commit("ghost")
	check("提交未知标识", err, ErrUnknownTx)
	check("回滚未知标识", l.Rollback("ghost"), ErrUnknownTx)
	check("重复发送（终态后标识仍保留）", l.Send("C1", "p", 0), ErrDuplicateTx)
	check("重复发送（回滚后）", l.Send("R1", "p", 0), ErrDuplicateTx)
	_, err = l.Commit("C1")
	check("对已提交再次提交", err, ErrAlreadyCommitted)
	check("对已提交再次回滚", l.Rollback("C1"), ErrAlreadyCommitted)
	_, err = l.Commit("R1")
	check("对已回滚再次提交", err, ErrAlreadyRolledBack)
	check("对已回滚再次回滚", l.Rollback("R1"), ErrAlreadyRolledBack)

	recC, _ := l.Get("C1")
	recR, _ := l.Get("R1")
	entries := l.Entries()
	if recC.State != StateCommitted || recC.Offset != 0 || recC.CheckCount != 0 {
		t.Fatalf("被拒绝的操作不应改变 C1: %+v", recC)
	}
	if recR.State != StateRolledBack || recR.RollbackReason != ReasonExplicit || recR.CheckCount != 0 {
		t.Fatalf("被拒绝的操作不应改变 R1: %+v", recR)
	}
	if len(entries) != 1 || entries[0].TxID != "C1" {
		t.Fatalf("被拒绝的操作不应改变日志: %+v", entries)
	}
}

// 时钟倒退整体拒绝且不改变状态；now 相等允许。
func TestClockBackwardRejected(t *testing.T) {
	l := mustNew(t, 0, 10, 3)
	mustSend(t, l, "A", "pa", 100)

	if err := l.Send("B", "pb", 99); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("now 倒退应拒绝: %v", err)
	}
	if _, ok := l.Get("B"); ok {
		t.Fatal("被拒绝的发送不应留下消息")
	}
	if err := l.Tick(50, countingCB(new(int), DecisionUnknown)); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("Tick 时钟倒退应拒绝: %v", err)
	}
	rec, _ := l.Get("A")
	if rec.CheckCount != 0 {
		t.Fatalf("被拒绝的 Tick 不应触发回查: %+v", rec)
	}
	// now 相等允许。
	if err := l.Send("B", "pb", 100); err != nil {
		t.Fatalf("now 相等应允许: %v", err)
	}
	mustTick(t, l, 100, countingCB(new(int), DecisionUnknown))
}

// 构造参数校验：F>=0、I>=1、M>=1。
func TestInvalidParams(t *testing.T) {
	for _, c := range []struct {
		f, i int64
		m    int
	}{
		{-1, 1, 1},
		{0, 0, 1},
		{0, 1, 0},
		{-5, -5, -5},
	} {
		if _, err := New(c.f, c.i, c.m); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d,%d,%d) 应拒绝: %v", c.f, c.i, c.m, err)
		}
	}
	if _, err := New(0, 1, 1); err != nil {
		t.Fatalf("New(0,1,1) 应允许: %v", err)
	}
}

// ---------- 朴素模拟：按规格逐条规则写成的参照实现 ----------

type naiveMsg struct {
	state       State
	offset      int
	createdAt   int64
	nextCheckAt int64
	checkCount  int
	reason      RollbackReason
}

// naiveModel 是规格的直接逐步模拟，单线程、无锁，用于对照真实实现。
type naiveModel struct {
	f, i    int64
	m       int
	msgs    map[string]*naiveMsg
	order   []string
	entries []Entry
}

func newNaiveModel(f, i int64, m int) *naiveModel {
	return &naiveModel{f: f, i: i, m: m, msgs: make(map[string]*naiveMsg)}
}

func (n *naiveModel) send(txID, payload string, now int64) {
	n.msgs[txID] = &naiveMsg{state: StatePending, offset: -1, createdAt: now, nextCheckAt: now + n.f}
	n.order = append(n.order, txID)
}

func (n *naiveModel) commit(txID string) {
	msg := n.msgs[txID]
	msg.state = StateCommitted
	msg.offset = len(n.entries)
	n.entries = append(n.entries, Entry{Offset: msg.offset, TxID: txID, Payload: ""})
}

func (n *naiveModel) rollback(txID string, reason RollbackReason) {
	msg := n.msgs[txID]
	msg.state = StateRolledBack
	msg.reason = reason
}

// tick 逐步模拟：先在推进开始时刻确定到点集合（按创建先后），
// 再逐条处理；每条至多一次；终态跳过不计次；第 m 次未知即耗尽回滚。
func (n *naiveModel) tick(now int64, decide func(txID string) Decision, logf func(format string, args ...any)) {
	var due []string
	for _, id := range n.order {
		msg := n.msgs[id]
		if msg.state == StatePending && msg.nextCheckAt <= now {
			due = append(due, id)
		}
	}
	logf("tick(now=%d) 到点集合=%v", now, due)
	for _, id := range due {
		msg := n.msgs[id]
		if msg.state != StatePending {
			logf("  %s: 已进入终态 %s, 跳过不计次", id, msg.state)
			continue
		}
		msg.checkCount++
		d := decide(id)
		logf("  %s: 第 %d 次回查, 决策=%v", id, msg.checkCount, d)
		switch d {
		case DecisionCommit:
			n.commit(id)
		case DecisionRollback:
			n.rollback(id, ReasonExplicit)
		default:
			if msg.checkCount >= n.m {
				n.rollback(id, ReasonCheckExhausted)
				logf("  %s: 第 %d 次仍未知, 回查耗尽回滚", id, msg.checkCount)
			} else {
				msg.nextCheckAt = now + n.i
				logf("  %s: 未知, 下次到点=%d(实际回查时刻 %d + I)", id, msg.nextCheckAt, now)
			}
		}
	}
}

// 与朴素模拟对照：相同调用序列与回调返回序列重放得到完全相同的日志与终态。
func TestCompareWithNaiveModel(t *testing.T) {
	const F, I, M = 10, 7, 3
	l := mustNew(t, F, I, M)
	n := newNaiveModel(F, I, M)

	// 回调返回序列（输入的一部分）：按事务标识依次弹出。
	script := map[string][]Decision{
		"A": {DecisionUnknown, DecisionCommit},
		"B": {DecisionUnknown, DecisionUnknown, DecisionUnknown},
	}
	realDecide := func(txID, _ string) Decision {
		d := script[txID][0]
		script[txID] = script[txID][1:]
		return d
	}
	naiveScript := map[string][]Decision{
		"A": {DecisionUnknown, DecisionCommit},
		"B": {DecisionUnknown, DecisionUnknown, DecisionUnknown},
	}
	naiveDecide := func(txID string) Decision {
		d := naiveScript[txID][0]
		naiveScript[txID] = naiveScript[txID][1:]
		return d
	}

	logf := func(format string, args ...any) { t.Logf("模拟: "+format, args...) }

	type op struct {
		desc string
		run  func()
	}
	ops := []op{
		{"send A@0", func() { mustSend(t, l, "A", "pa", 0); n.send("A", "pa", 0) }},
		{"send B@5", func() { mustSend(t, l, "B", "pb", 5); n.send("B", "pb", 5) }},
		{"send C@10", func() { mustSend(t, l, "C", "pc", 10); n.send("C", "pc", 10) }},
		{"commit C", func() {
			if _, err := l.Commit("C"); err != nil {
				t.Fatalf("Commit(C): %v", err)
			}
			n.commit("C")
		}},
		{"send E@20", func() { mustSend(t, l, "E", "pe", 20); n.send("E", "pe", 20) }},
		{"tick@20", func() { mustTick(t, l, 20, realDecide); n.tick(20, naiveDecide, logf) }},
		{"rollback E", func() {
			if err := l.Rollback("E"); err != nil {
				t.Fatalf("Rollback(E): %v", err)
			}
			n.rollback("E", ReasonExplicit)
		}},
		{"tick@27", func() { mustTick(t, l, 27, realDecide); n.tick(27, naiveDecide, logf) }},
		{"send D@35", func() { mustSend(t, l, "D", "pd", 35); n.send("D", "pd", 35) }},
		{"tick@40", func() { mustTick(t, l, 40, realDecide); n.tick(40, naiveDecide, logf) }},
		{"commit D", func() {
			if _, err := l.Commit("D"); err != nil {
				t.Fatalf("Commit(D): %v", err)
			}
			n.commit("D")
		}},
	}
	for _, o := range ops {
		t.Logf("输入: %s", o.desc)
		o.run()
	}

	realEntries := l.Entries()
	t.Logf("输出: 真实日志=%v", realEntries)
	t.Logf("输出: 模拟日志=%v", n.entries)
	if len(realEntries) != len(n.entries) {
		t.Fatalf("日志长度不一致: 真实 %d, 模拟 %d", len(realEntries), len(n.entries))
	}
	for idx, e := range n.entries {
		if realEntries[idx].Offset != e.Offset || realEntries[idx].TxID != e.TxID {
			t.Fatalf("日志位点 %d 不一致: 真实 %+v, 模拟 %+v", idx, realEntries[idx], e)
		}
	}

	for _, id := range n.order {
		rec, _ := l.Get(id)
		sim := n.msgs[id]
		t.Logf("终态对照 %s: 真实=(%s, 位点 %d, 回查 %d 次, 原因 %q) 模拟=(%s, 位点 %d, 回查 %d 次, 原因 %q)",
			id, rec.State, rec.Offset, rec.CheckCount, rec.RollbackReason,
			sim.state, sim.offset, sim.checkCount, sim.reason)
		if rec.State != sim.state || rec.Offset != sim.offset ||
			rec.CheckCount != sim.checkCount || rec.RollbackReason != sim.reason {
			t.Fatalf("%s 终态不一致: 真实 %+v, 模拟 %+v", id, rec, sim)
		}
	}
	t.Log("判定依据: 相同调用序列与回调返回序列下, 真实实现与朴素模拟的日志与终态完全一致")
}

// 并发调用：结果等价于某个串行顺序；位点连续无空洞；每个事务恰好一种终态。
func TestConcurrentUse(t *testing.T) {
	l := mustNew(t, 0, 1, 1) // M=1: 回查返回未知即耗尽回滚
	const senders = 8
	const perSender = 50

	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for k := 0; k < perSender; k++ {
				id := fmt.Sprintf("tx-%d-%d", s, k)
				if err := l.Send(id, "p", 0); err != nil {
					t.Errorf("Send(%s): %v", id, err)
					return
				}
				// 部分直接提交，部分直接回滚，其余留给回查。
				switch k % 3 {
				case 0:
					_, _ = l.Commit(id) // 可能与并发回查竞争，终态冲突属预期
				case 1:
					_ = l.Rollback(id)
				}
			}
		}(s)
	}
	// 并发推进：回调按标识散列确定性地提交或返回未知（M=1 即耗尽回滚）。
	stop := make(chan struct{})
	var tickerWg sync.WaitGroup
	tickerWg.Add(1)
	go func() {
		defer tickerWg.Done()
		cb := func(txID, _ string) Decision {
			if txID[len(txID)-1]%2 == 0 {
				return DecisionCommit
			}
			return DecisionUnknown
		}
		for {
			select {
			case <-stop:
				return
			default:
				_ = l.Tick(0, cb)
			}
		}
	}()
	wg.Wait()
	close(stop)
	tickerWg.Wait()

	// 收尾：反复推进直到没有待定消息。
	cb := func(txID, _ string) Decision { return DecisionCommit }
	for i := 0; i < senders*perSender+2; i++ {
		pending := 0
		for s := 0; s < senders; s++ {
			for k := 0; k < perSender; k++ {
				rec, _ := l.Get(fmt.Sprintf("tx-%d-%d", s, k))
				if rec.State == StatePending {
					pending++
				}
			}
		}
		if pending == 0 {
			break
		}
		mustTick(t, l, 0, cb)
	}

	entries := l.Entries()
	for idx, e := range entries {
		if e.Offset != idx {
			t.Fatalf("位点应连续无空洞: entries[%d].Offset=%d", idx, e.Offset)
		}
	}
	committed := 0
	rolledBack := 0
	for s := 0; s < senders; s++ {
		for k := 0; k < perSender; k++ {
			id := fmt.Sprintf("tx-%d-%d", s, k)
			rec, ok := l.Get(id)
			if !ok {
				t.Fatalf("%s 丢失", id)
			}
			switch rec.State {
			case StateCommitted:
				committed++
				if rec.Offset < 0 || rec.Offset >= len(entries) || entries[rec.Offset].TxID != id {
					t.Fatalf("%s 位点与日志不一致: %+v", id, rec)
				}
			case StateRolledBack:
				rolledBack++
				if rec.RollbackReason != ReasonExplicit && rec.RollbackReason != ReasonCheckExhausted {
					t.Fatalf("%s 回滚原因未知: %+v", id, rec)
				}
			default:
				t.Fatalf("%s 未进入终态: %+v", id, rec)
			}
		}
	}
	t.Logf("输出: 已提交=%d, 已回滚=%d, 日志条数=%d；判定依据: 位点连续无空洞且每个事务恰好一种终态",
		committed, rolledBack, len(entries))
	if committed != len(entries) {
		t.Fatalf("已提交数应等于日志条数: %d != %d", committed, len(entries))
	}
	if committed+rolledBack != senders*perSender {
		t.Fatalf("终态总数不等于发送总数: %d+%d != %d", committed, rolledBack, senders*perSender)
	}
}
