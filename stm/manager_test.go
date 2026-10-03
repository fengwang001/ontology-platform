package stm

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func newManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager(%+v) 被拒: %v", cfg, err)
	}
	return m
}

func mustOpen(t *testing.T, m *Manager, id, o int, write bool) OpenResult {
	t.Helper()
	r, err := m.Open(id, o, write)
	if err != nil {
		t.Fatalf("Open(%d,%d,%v) 被拒: %v", id, o, write, err)
	}
	return r
}

func mustScore(t *testing.T, m *Manager, id int) (int, int) {
	t.Helper()
	kp, ab, err := m.Score(id)
	if err != nil {
		t.Fatalf("Score(%d) 被拒: %v", id, err)
	}
	return kp, ab
}

func mustRestart(t *testing.T, m *Manager, id int) int {
	t.Helper()
	d, err := m.Restart(id)
	if err != nil {
		t.Fatalf("Restart(%d) 被拒: %v", id, err)
	}
	return d
}

func openN(t *testing.T, m *Manager, id int, objs ...int) {
	t.Helper()
	for _, o := range objs {
		if r := mustOpen(t, m, id, o, true); !r.Acquired {
			t.Fatalf("Open(%d,%d,写) 应直接获得", id, o)
		}
	}
}

// 驱动 id 对 o 的写冲突直到获胜，返回每次 Open 的结果序列。
func driveUntilWin(t *testing.T, m *Manager, id, o int, max int) []OpenResult {
	t.Helper()
	var rs []OpenResult
	for i := 0; i < max; i++ {
		r := mustOpen(t, m, id, o, true)
		rs = append(rs, r)
		if r.Acquired {
			return rs
		}
	}
	t.Fatalf("事务 %d 在 %d 次尝试内未获胜", id, max)
	return nil
}

func delays(rs []OpenResult) []int {
	var ds []int
	for _, r := range rs {
		if !r.Acquired {
			ds = append(ds, r.Delay)
		}
	}
	return ds
}

// TestSpecExample 逐步复现题目给出的示例。
func TestSpecExample(t *testing.T) {
	m := newManager(t, Config{M: 2, L: 2, D: 10, E: 3, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()

	openN(t, m, t1, 0)
	openN(t, m, t2, 1)
	for _, id := range []int{t1, t2} {
		if kp, _ := mustScore(t, m, id); kp != 1 {
			t.Fatalf("T%d kp 应为 1", id)
		}
	}

	r := mustOpen(t, m, t2, 0, true) // k=0: 1+0 不大于 1
	if r.Acquired || r.Delay != 10 {
		t.Fatalf("k=0 应等待 10, 得 %+v", r)
	}
	r = mustOpen(t, m, t2, 0, true) // k=1: 1+1=2 > 1
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t1}) {
		t.Fatalf("k=1 应中止 T1, 得 %+v", r)
	}
	if kp, ab := mustScore(t, m, t1); kp != 1 || ab != 1 {
		t.Fatalf("T1 应 kp=1 ab=1, 得 kp=%d ab=%d", kp, ab)
	}
	if kp, _ := mustScore(t, m, t2); kp != 2 {
		t.Fatalf("T2 kp 应为 2")
	}
	if d := mustRestart(t, m, t1); d != 10 {
		t.Fatalf("T1 Restart 延迟=%d, 期望 10", d)
	}

	rs := driveUntilWin(t, m, t1, 0, 3)
	if got := delays(rs); !reflect.DeepEqual(got, []int{10, 20}) {
		t.Fatalf("T1 重试延迟应为 [10 20], 得 %v", got)
	}
	if last := rs[len(rs)-1]; !reflect.DeepEqual(last.Aborted, []int{t2}) {
		t.Fatalf("k=2 应中止 T2, 得 %+v", last)
	}
	if kp, ab := mustScore(t, m, t2); kp != 1 || ab != 1 {
		t.Fatalf("T2 应 kp=1 ab=1, 得 kp=%d ab=%d", kp, ab)
	}
	if kp, _ := mustScore(t, m, t1); kp != 2 {
		t.Fatalf("T1 kp 应为 2")
	}
}

// TestScoreTieLosesMarginWins 积分+k 恰等不胜，超出 1 才胜。
func TestScoreTieLosesMarginWins(t *testing.T) {
	m := newManager(t, Config{M: 1, L: 16, D: 5, E: 3, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0) // T1 kp=1

	rs := driveUntilWin(t, m, t2, 0, 3)
	if len(rs) != 3 {
		t.Fatalf("应第 3 次才胜, 实际 %d 次", len(rs))
	}
	// k=1 时 kp(t)+k == kp(e) 恰等，不胜；k=2 时超出 1，胜。
	if rs[0].Acquired || rs[1].Acquired || !rs[2].Acquired {
		t.Fatalf("胜负序列错误: %+v", rs)
	}
	if !reflect.DeepEqual(rs[2].Aborted, []int{t1}) {
		t.Fatalf("应中止 T1: %+v", rs[2])
	}
}

// TestAttAccumulates 弱者靠 att 累加最终压过强者。
func TestAttAccumulates(t *testing.T) {
	m := newManager(t, Config{M: 4, L: 16, D: 1, E: 20, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1, 2) // T1 kp=3

	rs := driveUntilWin(t, m, t2, 0, 5) // kp(t)=0, 需 0+k>3 即 k=4
	if len(rs) != 5 {
		t.Fatalf("应第 5 次才胜, 实际 %d 次", len(rs))
	}
	if got := delays(rs); !reflect.DeepEqual(got, []int{1, 2, 4, 8}) {
		t.Fatalf("等待延迟应为 [1 2 4 8], 得 %v", got)
	}
	if !reflect.DeepEqual(rs[4].Aborted, []int{t1}) {
		t.Fatalf("应中止 T1: %+v", rs[4])
	}
}

// TestAttClearedOnAcquireAndRestart att 在获得后清零，且 Restart 清零。
func TestAttClearedOnAcquireAndRestart(t *testing.T) {
	m := newManager(t, Config{M: 2, L: 16, D: 10, E: 5, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0) // T1 kp=1
	openN(t, m, t2, 1) // T2 kp=1

	rs := driveUntilWin(t, m, t2, 0, 2) // k=0 败, k=1 胜
	if len(rs) != 2 {
		t.Fatalf("应第 2 次胜, 实际 %d 次", len(rs))
	}
	if got := m.txns[t2-1].att[0]; got != 0 {
		t.Fatalf("获得后 att[0] 应清零, 得 %d", got)
	}

	mustRestart(t, m, t1)
	rs = driveUntilWin(t, m, t1, 1, 3) // T1 kp=1 vs T2 kp=2: k=2 胜
	if len(rs) != 3 {
		t.Fatalf("应第 3 次胜, 实际 %d 次", len(rs))
	}
	// T2 被中止时 att 已清零；Restart 再次清零。
	mustRestart(t, m, t2)
	for o := 0; o < 2; o++ {
		if got := m.txns[t2-1].att[o]; got != 0 {
			t.Fatalf("Restart 后 att[%d] 应清零, 得 %d", o, got)
		}
	}
	// 可观察证据：重新冲突的首次延迟按 k=0 计算。
	r := mustOpen(t, m, t2, 1, true)
	if r.Acquired || r.Delay != 10 {
		t.Fatalf("att 清零后首次等待应为 10, 得 %+v", r)
	}
}

// TestHalvingOddEven 被中止时积分对奇数与偶数都取上整一半。
func TestHalvingOddEven(t *testing.T) {
	// 奇数：3 -> 2
	m := newManager(t, Config{M: 4, L: 16, D: 1, E: 5, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1, 2) // kp=3
	driveUntilWin(t, m, t2, 0, 5)
	if kp, ab := mustScore(t, m, t1); kp != 2 || ab != 1 {
		t.Fatalf("奇数减半应得 kp=2 ab=1, 得 kp=%d ab=%d", kp, ab)
	}
	// 偶数：2 -> 1
	m2 := newManager(t, Config{M: 2, L: 16, D: 1, E: 5, P: 100, Q: 16})
	a, b := m2.Begin(), m2.Begin()
	openN(t, m2, a, 0, 1) // kp=2
	driveUntilWin(t, m2, b, 0, 4)
	if kp, ab := mustScore(t, m2, a); kp != 1 || ab != 1 {
		t.Fatalf("偶数减半应得 kp=1 ab=1, 得 kp=%d ab=%d", kp, ab)
	}
}

// TestRestartKeepsScoreClearsHolds 重启后积分保留而持有清空。
func TestRestartKeepsScoreClearsHolds(t *testing.T) {
	m := newManager(t, Config{M: 2, L: 16, D: 10, E: 3, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1) // kp=2
	driveUntilWin(t, m, t2, 0, 4)
	mustRestart(t, m, t1)
	if kp, ab := mustScore(t, m, t1); kp != 1 || ab != 1 {
		t.Fatalf("重启后应 kp=1 ab=1, 得 kp=%d ab=%d", kp, ab)
	}
	for o := 0; o < 2; o++ {
		if h := m.txns[t1-1].holds[o]; h != ModeNone {
			t.Fatalf("重启后持有应清空, holds[%d]=%v", o, h)
		}
	}
	// 积分保留的进一步证据：T1 重新获得空闲对象 1 后 kp=2。
	openN(t, m, t1, 1)
	if kp, _ := mustScore(t, m, t1); kp != 2 {
		t.Fatalf("重新获得后 kp 应为 2")
	}
	_ = t2
}

// TestReadWriteUpgradeNoExtraPoint 读升级为写不再加分。
func TestReadWriteUpgradeNoExtraPoint(t *testing.T) {
	m := newManager(t, Config{M: 1, L: 16, D: 1, E: 1, P: 100, Q: 16})
	t1 := m.Begin()
	if r := mustOpen(t, m, t1, 0, false); !r.Acquired {
		t.Fatalf("读开应获得")
	}
	if kp, _ := mustScore(t, m, t1); kp != 1 {
		t.Fatalf("读开后 kp 应为 1")
	}
	// 重复读开：无变化。
	if r := mustOpen(t, m, t1, 0, false); !r.Acquired || len(r.Aborted) != 0 {
		t.Fatalf("重复读开应无变化")
	}
	// 升级为写：获得但不加分。
	if r := mustOpen(t, m, t1, 0, true); !r.Acquired {
		t.Fatalf("升级写应获得")
	}
	if kp, _ := mustScore(t, m, t1); kp != 1 {
		t.Fatalf("读升级为写不应加分, kp=%d", kp)
	}
	if h := m.txns[t1-1].holds[0]; h != ModeWrite {
		t.Fatalf("应持有写, 得 %v", h)
	}
	// 已持有写再求读：无变化。
	if r := mustOpen(t, m, t1, 0, false); !r.Acquired {
		t.Fatalf("持有写时求读应无变化")
	}
	if kp, _ := mustScore(t, m, t1); kp != 1 {
		t.Fatalf("持有写时求读不应加分, kp=%d", kp)
	}
}

// TestCappedScoreTieLoses 积分封顶后与敌手持平不胜。
func TestCappedScoreTieLoses(t *testing.T) {
	m := newManager(t, Config{M: 5, L: 16, D: 1, E: 5, P: 2, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1, 2) // kp  capped at 2
	if kp, _ := mustScore(t, m, t1); kp != 2 {
		t.Fatalf("kp 应封顶为 2, 得 %d", kp)
	}
	openN(t, m, t2, 3, 4) // kp=2
	// k=0: kp(t)+k == 2 == kp(e), 持平不胜。
	r := mustOpen(t, m, t2, 0, true)
	if r.Acquired {
		t.Fatalf("封顶后持平不应获胜")
	}
	// k=1: 2+1 > 2, 胜。
	r = mustOpen(t, m, t2, 0, true)
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t1}) {
		t.Fatalf("k=1 应胜, 得 %+v", r)
	}
}

// TestKEqualsQ k 恰达 Q 时压过非特权敌手，却压不过特权敌手。
func TestKEqualsQ(t *testing.T) {
	// 非特权敌手：k==Q 即胜，无需积分优势。
	m := newManager(t, Config{M: 4, L: 2, D: 1, E: 5, P: 100, Q: 3})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1, 2) // kp=3, T2 靠积分永远不够(0+k>3 需 k=4)
	rs := driveUntilWin(t, m, t2, 0, 4)
	if len(rs) != 4 || !rs[3].Acquired {
		t.Fatalf("k 恰达 Q=3 时应胜, 序列 %+v", rs)
	}
	// 特权敌手：k>=Q 也败。
	m2 := newManager(t, Config{M: 2, L: 1, D: 1, E: 5, P: 100, Q: 1})
	a, b, c := m2.Begin(), m2.Begin(), m2.Begin()
	openN(t, m2, a, 0)            // a kp=1
	driveUntilWin(t, m2, b, 0, 3) // k=1>=Q, a 被中止, ab=1>=L=1 成特权
	mustRestart(t, m2, a)
	openN(t, m2, a, 1)       // a kp=2, 持有 1
	for i := 0; i < 3; i++ { // c 非特权, k 已达 Q 仍败
		if r := mustOpen(t, m2, c, 1, true); r.Acquired {
			t.Fatalf("k>=%d 也压不过特权敌手, 第 %d 次竟胜", 1, i)
		}
	}
}

// TestWriteMultipleReaders 求写面对多个读者须压过全部，否则一个都不中止。
func TestWriteMultipleReaders(t *testing.T) {
	m := newManager(t, Config{M: 2, L: 16, D: 1, E: 5, P: 100, Q: 16})
	t1, t2, t3, t4 := m.Begin(), m.Begin(), m.Begin(), m.Begin()
	for _, id := range []int{t1, t2, t3} {
		if r := mustOpen(t, m, id, 0, false); !r.Acquired {
			t.Fatalf("T%d 读开应获得", id)
		}
	}
	openN(t, m, t3, 1) // T3 kp=2, 其余 kp=1

	mustOpen(t, m, t4, 0, true) // k=0: 0>1 否
	mustOpen(t, m, t4, 0, true) // k=1: 1>1 否
	r := mustOpen(t, m, t4, 0, true)
	if r.Acquired {
		t.Fatalf("k=2 压不过 T3(2>2 否), 不应获得")
	}
	// 一个都压不过全部时，任何读者都不被中止。
	for _, id := range []int{t1, t2, t3} {
		if m.txns[id-1].st != stActive || m.txns[id-1].holds[0] != ModeRead {
			t.Fatalf("T%d 不应被中止或释放", id)
		}
		if _, ab := mustScore(t, m, id); ab != 0 {
			t.Fatalf("T%d ab 应为 0", id)
		}
	}
	r = mustOpen(t, m, t4, 0, true) // k=3: 压过全部
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t1, t2, t3}) {
		t.Fatalf("k=3 应中止全部读者, 得 %+v", r)
	}
	if h := m.txns[t4-1].holds[0]; h != ModeWrite {
		t.Fatalf("T4 应持有写")
	}
}

// TestPrivilegedBeatsNonPrivileged 特权对非特权必胜。
func TestPrivilegedBeatsNonPrivileged(t *testing.T) {
	m := newManager(t, Config{M: 1, L: 1, D: 1, E: 5, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0)            // kp=1
	driveUntilWin(t, m, t2, 0, 3) // k=2 胜, T1 ab=1>=L=1 成特权
	mustRestart(t, m, t1)
	// T1 特权, kp=1 与 T2 kp=1 持平, 仍在 k=0 直接胜。
	r := mustOpen(t, m, t1, 0, true)
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t2}) {
		t.Fatalf("特权对非特权应 k=0 必胜, 得 %+v", r)
	}
}

// TestBothPrivilegedByTxnID 双特权按事务号小者胜。
func TestBothPrivilegedByTxnID(t *testing.T) {
	m := newManager(t, Config{M: 2, L: 1, D: 1, E: 5, P: 100, Q: 16})
	t1, t2, t3 := m.Begin(), m.Begin(), m.Begin()
	openN(t, m, t1, 0)            // kp=1
	openN(t, m, t2, 1)            // kp=1
	driveUntilWin(t, m, t3, 0, 3) // T1 ab=1 成特权
	driveUntilWin(t, m, t3, 1, 3) // T2 ab=1 成特权
	mustRestart(t, m, t1)
	mustRestart(t, m, t2)

	openN(t, m, t1, 0) // T1 特权压过非特权 T3, 夺回 0
	openN(t, m, t2, 1) // T2 重新持有 1(T3 已被中止)
	r := mustOpen(t, m, t1, 1, true)
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t2}) {
		t.Fatalf("双特权小号 T1 应胜, 得 %+v", r)
	}
	mustRestart(t, m, t2)
	// T2 大号挑战 T1：无论 k 如何都败。
	for i, want := range []int{1, 2, 4} {
		r := mustOpen(t, m, t2, 0, true)
		if r.Acquired || r.Delay != want {
			t.Fatalf("第 %d 次应等待 %d, 得 %+v", i, want, r)
		}
	}
}

// TestPrivilegeFromNextConflict ab 恰达 L 的下一次冲突起生效。
func TestPrivilegeFromNextConflict(t *testing.T) {
	m := newManager(t, Config{M: 3, L: 2, D: 1, E: 5, P: 100, Q: 16})
	t1, t2, t3 := m.Begin(), m.Begin(), m.Begin()
	openN(t, m, t1, 0)            // kp=1
	driveUntilWin(t, m, t2, 0, 3) // T1 ab=1 < L=2, 尚非特权
	mustRestart(t, m, t1)
	openN(t, m, t1, 1)            // kp=2
	driveUntilWin(t, m, t3, 1, 4) // k=3 胜, T1 ab=2=L 成特权
	if _, ab := mustScore(t, m, t1); ab != 2 {
		t.Fatalf("T1 ab 应为 2")
	}
	mustRestart(t, m, t1)
	// 下一次冲突起生效：T1 kp=1 对 T3 kp=3, 非特权规则需 k>=3, 特权则 k=0 即胜。
	r := mustOpen(t, m, t1, 1, true)
	if !r.Acquired || !reflect.DeepEqual(r.Aborted, []int{t3}) {
		t.Fatalf("ab 达 L 后应 k=0 即胜, 得 %+v", r)
	}
}

// TestDelayCap 延迟指数按 E 封顶。
func TestDelayCap(t *testing.T) {
	m := newManager(t, Config{M: 5, L: 16, D: 10, E: 2, P: 100, Q: 16})
	t1, t2 := m.Begin(), m.Begin()
	openN(t, m, t1, 0, 1, 2, 3, 4) // kp=5, T2 需 k=6 才胜
	var got []int
	for i := 0; i < 5; i++ {
		r := mustOpen(t, m, t2, 0, true)
		if r.Acquired {
			t.Fatalf("第 %d 次不应获胜", i)
		}
		got = append(got, r.Delay)
	}
	if want := []int{10, 20, 40, 40, 40}; !reflect.DeepEqual(got, want) {
		t.Fatalf("延迟应为 %v, 得 %v", want, got)
	}
}

// TestRestartDelayUsesAbMinusOne Restart 延迟用 ab-1 作指数。
func TestRestartDelayUsesAbMinusOne(t *testing.T) {
	m := newManager(t, Config{M: 3, L: 16, D: 10, E: 20, P: 100, Q: 16})
	t1 := m.Begin()
	openN(t, m, t1, 0) // kp=1
	attackers := []int{m.Begin(), m.Begin(), m.Begin()}
	want := []int{10, 20, 40} // ab=1,2,3 -> 2^(ab-1)
	for i, a := range attackers {
		driveUntilWin(t, m, a, i, 5)
		if d := mustRestart(t, m, t1); d != want[i] {
			t.Fatalf("ab=%d 时 Restart 延迟应为 %d, 得 %d", i+1, want[i], d)
		}
		if i+1 < len(attackers) {
			openN(t, m, t1, i+1) // 重新持有下一个对象
		}
	}
	if _, ab := mustScore(t, m, t1); ab != 3 {
		t.Fatalf("T1 ab 应为 3")
	}
}

// TestRejections 拒绝原因顺序与被拒调用不改状态。
func TestRejections(t *testing.T) {
	// 配置非法整体拒绝。
	bad := []Config{
		{M: 0, L: 1, D: 1, E: 0, P: 1, Q: 1},
		{M: 65, L: 1, D: 1, E: 0, P: 1, Q: 1},
		{M: 1, L: 0, D: 1, E: 0, P: 1, Q: 1},
		{M: 1, L: 17, D: 1, E: 0, P: 1, Q: 1},
		{M: 1, L: 1, D: 0, E: 0, P: 1, Q: 1},
		{M: 1, L: 1, D: 1001, E: 0, P: 1, Q: 1},
		{M: 1, L: 1, D: 1, E: -1, P: 1, Q: 1},
		{M: 1, L: 1, D: 1, E: 21, P: 1, Q: 1},
		{M: 1, L: 1, D: 1, E: 0, P: 0, Q: 1},
		{M: 1, L: 1, D: 1, E: 0, P: 1001, Q: 1},
		{M: 1, L: 1, D: 1, E: 0, P: 1, Q: 0},
		{M: 1, L: 1, D: 1, E: 0, P: 1, Q: 17},
	}
	for _, cfg := range bad {
		if _, err := NewManager(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("配置 %+v 应报配置非法, 得 %v", cfg, err)
		}
	}
	for _, cfg := range []Config{
		{M: 1, L: 1, D: 1, E: 0, P: 1, Q: 1},
		{M: 64, L: 16, D: 1000, E: 20, P: 1000, Q: 16},
	} {
		if _, err := NewManager(cfg); err != nil {
			t.Fatalf("边界合法配置 %+v 不应被拒: %v", cfg, err)
		}
	}

	m := newManager(t, Config{M: 2, L: 2, D: 10, E: 3, P: 100, Q: 16})
	// 事务号不存在（优先于其他原因）。
	if _, err := m.Open(1, 0, true); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("未 Begin 应报事务号不存在, 得 %v", err)
	}
	t1 := m.Begin()
	for _, id := range []int{0, -1, 2, 99} {
		if _, err := m.Open(id, 99, true); !errors.Is(err, ErrNoSuchTxn) {
			t.Fatalf("Open(%d) 应报事务号不存在, 得 %v", id, err)
		}
		if err := m.Commit(id); !errors.Is(err, ErrNoSuchTxn) {
			t.Fatalf("Commit(%d) 应报事务号不存在, 得 %v", id, err)
		}
		if _, err := m.Restart(id); !errors.Is(err, ErrNoSuchTxn) {
			t.Fatalf("Restart(%d) 应报事务号不存在, 得 %v", id, err)
		}
	}
	// 状态不符：Restart 须已中止。
	if _, err := m.Restart(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("活跃事务 Restart 应报状态不符, 得 %v", err)
	}
	// 对象越界（仅 Open，且排在状态之后）。
	if _, err := m.Open(t1, 2, true); !errors.Is(err, ErrBadObject) {
		t.Fatalf("对象越界应报对象越界, 得 %v", err)
	}
	if _, err := m.Open(t1, -1, true); !errors.Is(err, ErrBadObject) {
		t.Fatalf("负对象应报对象越界, 得 %v", err)
	}
	// 提交后一切调用须报状态不符。
	if err := m.Commit(t1); err != nil {
		t.Fatalf("Commit 应成功: %v", err)
	}
	if _, err := m.Open(t1, 99, true); !errors.Is(err, ErrBadState) {
		t.Fatalf("已提交 Open 应报状态不符(优先于对象越界), 得 %v", err)
	}
	if err := m.Commit(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("重复 Commit 应报状态不符, 得 %v", err)
	}
	if err := m.Abort(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("已提交 Abort 应报状态不符, 得 %v", err)
	}
	if _, err := m.Restart(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("已提交 Restart 应报状态不符, 得 %v", err)
	}
	// 自愿中止后可 Restart，且 ab 不变。
	t2 := m.Begin()
	if err := m.Abort(t2); err != nil {
		t.Fatalf("Abort 应成功: %v", err)
	}
	if _, err := m.Open(t2, 0, true); !errors.Is(err, ErrBadState) {
		t.Fatalf("已中止 Open 应报状态不符, 得 %v", err)
	}
	if _, ab := mustScore(t, m, t2); ab != 0 {
		t.Fatalf("自愿中止不改 ab")
	}
	if d, err := m.Restart(t2); err != nil || d != 10 {
		t.Fatalf("Restart 应成功且延迟 10, 得 d=%d err=%v", d, err)
	}

	// 被拒调用不得改变任何状态：用可观察的 att 与延迟验证。
	t3, t4 := m.Begin(), m.Begin()
	openN(t, m, t3, 0)               // kp=1
	r := mustOpen(t, m, t4, 0, true) // k=0 等待, att=1
	if r.Acquired || r.Delay != 10 {
		t.Fatalf("应等待 10, 得 %+v", r)
	}
	if _, err := m.Open(t4, 99, true); !errors.Is(err, ErrBadObject) {
		t.Fatalf("应报对象越界, 得 %v", err)
	}
	if _, err := m.Open(99, 0, true); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("应报事务号不存在, 得 %v", err)
	}
	r = mustOpen(t, m, t4, 0, true) // att 未被拒调用改动: k=1
	if r.Acquired || r.Delay != 20 {
		t.Fatalf("被拒调用不得改 att, 应等待 20, 得 %+v", r)
	}
}

// TestConcurrent 并发调用等价于某串行顺序，且不变量成立。
func TestConcurrent(t *testing.T) {
	m := newManager(t, Config{M: 8, L: 2, D: 1, E: 3, P: 50, Q: 4})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				id := 1 + rng.Intn(24)
				switch rng.Intn(5) {
				case 0:
					m.Begin()
				case 1, 2:
					m.Open(id, rng.Intn(8), rng.Intn(2) == 0)
				case 3:
					m.Restart(id)
				case 4:
					if rng.Intn(2) == 0 {
						m.Commit(id)
					} else {
						m.Abort(id)
					}
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	for o := 0; o < m.cfg.M; o++ {
		writers, readers := 0, 0
		for _, tx := range m.txns {
			switch tx.holds[o] {
			case ModeWrite:
				writers++
			case ModeRead:
				readers++
			}
		}
		if writers > 1 || (writers == 1 && readers > 0) {
			t.Fatalf("对象 %d 违反写者唯一不变量: 写者 %d 读者 %d", o, writers, readers)
		}
	}
	for id, tx := range m.txns {
		if tx.kp < 0 || tx.kp > m.cfg.P {
			t.Fatalf("事务 %d kp=%d 越界", id+1, tx.kp)
		}
		if tx.ab < 0 {
			t.Fatalf("事务 %d ab<0", id+1)
		}
		if tx.st != stActive {
			for o := 0; o < m.cfg.M; o++ {
				if tx.holds[o] != ModeNone || tx.att[o] != 0 {
					t.Fatalf("非活跃事务 %d 不应有持有或 att", id+1)
				}
			}
		}
	}
}

// TestReplayDeterminism 相同调用序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	cfg := Config{M: 3, L: 2, D: 7, E: 2, P: 5, Q: 3}
	run := func() []any {
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var out []any
		rng := rand.New(rand.NewSource(42))
		ids := []int{}
		for i := 0; i < 200; i++ {
			switch rng.Intn(5) {
			case 0:
				ids = append(ids, m.Begin())
				out = append(out, ids[len(ids)-1])
			case 1, 2:
				id := 1 + rng.Intn(len(ids)+1)
				r, err := m.Open(id, rng.Intn(4), rng.Intn(2) == 0)
				out = append(out, r, err != nil)
			case 3:
				d, err := m.Restart(1 + rng.Intn(len(ids)+1))
				out = append(out, d, err != nil)
			case 4:
				var err error
				if rng.Intn(2) == 0 {
					err = m.Commit(1 + rng.Intn(len(ids)+1))
				} else {
					err = m.Abort(1 + rng.Intn(len(ids)+1))
				}
				out = append(out, err != nil)
			}
		}
		return out
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("重放结果不一致")
	}
}
