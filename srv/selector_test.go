package srv

import (
	"sync"
	"testing"
)

func mustNew(t *testing.T, capacity int, coolCap, wr int64) *Selector {
	t.Helper()
	s, err := New(capacity, coolCap, wr)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) 失败: %v", capacity, coolCap, wr, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Selector, target string, port, priority, weight int, ttl, now int64) {
	t.Helper()
	if err := s.Add(target, port, priority, weight, ttl, now); err != nil {
		t.Fatalf("Add(%s, %d) 失败: %v", target, port, err)
	}
}

func mustPick(t *testing.T, s *Selector, r uint64, now int64, wantTarget string, wantPort int) {
	t.Helper()
	target, port, err := s.Pick(r, now)
	if err != nil {
		t.Fatalf("Pick(%d, %d) 失败: %v", r, now, err)
	}
	if target != wantTarget || port != wantPort {
		t.Fatalf("Pick(%d, %d) = (%s, %d), 期望 (%s, %d)", r, now, target, port, wantTarget, wantPort)
	}
}

func checkErrCode(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %d，实际无错误", code)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型不是 *Error: %T", err)
	}
	if e.Code != code {
		t.Fatalf("错误码 = %d，期望 %d（%v）", e.Code, code, e)
	}
}

func (s *Selector) rec(t *testing.T, target string, port int) *record {
	t.Helper()
	rec, ok := s.records[key{target, port}]
	if !ok {
		t.Fatalf("记录 (%s, %d) 不存在", target, port)
	}
	return rec
}

// 题目主示例：组内排序、r 取模、权重 0 排最前。
func TestPickWeightedExample(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "R1", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "R2", 2, 10, 0, 1000, 0)
	mustAdd(t, s, "R3", 3, 10, 15, 1000, 0)
	mustAdd(t, s, "R4", 4, 20, 100, 1000, 0)

	// 组 10 顺序 R2、R1、R3，累计 0、5、20，S=20。
	mustPick(t, s, 0, 0, "R2", 2)  // r1=0 选权重 0 的 R2
	mustPick(t, s, 1, 0, "R1", 1)  // r1 在 1..5 选 R1
	mustPick(t, s, 5, 0, "R1", 1)  //
	mustPick(t, s, 6, 0, "R3", 3)  // r1 在 6..20 选 R3
	mustPick(t, s, 20, 0, "R3", 3) // r1 取到 S（上界）选最后一个有权重的记录
	mustPick(t, s, 21, 0, "R2", 2) // r=21 时 r1=0 选 R2
	mustPick(t, s, 41, 0, "R3", 3) // 41 mod 21 = 20，r1=20 选 R3
}

// 冷却恰在截止时刻恢复；冷却期间组内 S 变化；整组不可用降到下一优先级。
func TestCooldownBoundaryAndFallback(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "R1", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "R2", 2, 10, 0, 1000, 0)
	mustAdd(t, s, "R3", 3, 10, 15, 1000, 0)
	mustAdd(t, s, "R4", 4, 20, 100, 1000, 0)

	// R3 在 100 被设冷却 10（f=1），截止 110。
	if err := s.Failure("R3", 3, 10, 100); err != nil {
		t.Fatalf("Failure 失败: %v", err)
	}
	if got := s.rec(t, "R3", 3).cooldownUntil; got != 110 {
		t.Fatalf("R3 冷却截止 = %d，期望 110", got)
	}
	// 109 时 R3 不可用，组 10 的 S=5。
	mustPick(t, s, 5, 109, "R1", 1) // r1=5 选 R1
	mustPick(t, s, 6, 109, "R2", 2) // r1=0 选 R2
	// 110 时恰在截止时刻恢复。
	mustPick(t, s, 6, 110, "R3", 3)

	// 组 10 全部不可用时选 R4，任意 r 都得 R4。
	// CoolCap=60，冷却 1000 封顶为 60，截止 260，故在 259 检查。
	for _, target := range []struct {
		name string
		port int
	}{{"R1", 1}, {"R2", 2}, {"R3", 3}} {
		if err := s.Failure(target.name, target.port, 1000, 200); err != nil {
			t.Fatalf("Failure(%s) 失败: %v", target.name, err)
		}
	}
	for _, r := range []uint64{0, 1, 7, 100, 1 << 40} {
		mustPick(t, s, r, 259, "R4", 4)
	}
}

// 组内全为 0 权重时 S=0，任意 r 都选登记序最小者。
func TestAllZeroWeightGroup(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "A", 1, 5, 0, 1000, 0)
	mustAdd(t, s, "B", 2, 5, 0, 1000, 0)
	mustAdd(t, s, "C", 3, 5, 0, 1000, 0)
	for _, r := range []uint64{0, 1, 2, 999, 1 << 63} {
		mustPick(t, s, r, 0, "A", 1)
	}
}

// 到期恰在 now 等于到期时刻时不可用；Purge 删除到期时刻不大于 now 的记录。
func TestExpiryBoundary(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "A", 1, 10, 1, 100, 0) // 到期时刻 100
	mustPick(t, s, 0, 99, "A", 1)
	if _, _, err := s.Pick(0, 100); err == nil {
		t.Fatal("now=100 时期望无可用记录")
	} else {
		checkErrCode(t, err, ErrNoAvailable)
	}
	n, err := s.Purge(99)
	if err != nil || n != 0 {
		t.Fatalf("Purge(99) = (%d, %v)，期望 (0, nil)", n, err)
	}
	n, err = s.Purge(100)
	if err != nil || n != 1 {
		t.Fatalf("Purge(100) = (%d, %v)，期望 (1, nil)", n, err)
	}
}

// 重复登记保留登记序号、冷却截止、f 与 lf，覆盖 priority/weight 并更新到期时刻。
func TestReAddKeepsState(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "A", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "B", 2, 10, 5, 1000, 0)
	if err := s.Failure("A", 1, 10, 50); err != nil {
		t.Fatal(err)
	}
	before := s.rec(t, "A", 1)
	seq, until, f, lf, hasLf := before.seq, before.cooldownUntil, before.failures, before.lastFail, before.hasLastFail

	mustAdd(t, s, "A", 1, 20, 9, 500, 60) // 更新
	after := s.rec(t, "A", 1)
	if after.seq != seq {
		t.Fatalf("重复登记后序号 = %d，期望保留 %d", after.seq, seq)
	}
	if after.cooldownUntil != until || after.failures != f || after.lastFail != lf || after.hasLastFail != hasLf {
		t.Fatal("重复登记未保留冷却截止 / f / lf")
	}
	if after.priority != 20 || after.weight != 9 || after.expiresAt != 560 {
		t.Fatalf("更新未生效: priority=%d weight=%d expiresAt=%d", after.priority, after.weight, after.expiresAt)
	}
	// A 的 priority 已更新为 20，组 10 只剩 B。
	mustPick(t, s, 0, 70, "B", 2)
}

// 题目指数冷却例：CoolCap=60、Wr=100、cooldown=10，逐步验证 f、lf 与冷却截止。
func TestExponentialCooldownExample(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "R3", 3, 10, 15, 1_000_000_000, 0)
	rec := s.rec(t, "R3", 3)

	fail := func(now int64) {
		t.Helper()
		if err := s.Failure("R3", 3, 10, now); err != nil {
			t.Fatalf("Failure(now=%d) 失败: %v", now, err)
		}
	}
	check := func(now int64, f uint64, lf int64, until int64) {
		t.Helper()
		if rec.failures != f || rec.lastFail != lf || !rec.hasLastFail || rec.cooldownUntil != until {
			t.Fatalf("now=%d 后状态 (f=%d, lf=%d, until=%d)，期望 (f=%d, lf=%d, until=%d)",
				now, rec.failures, rec.lastFail, rec.cooldownUntil, f, lf, until)
		}
	}

	fail(100)
	check(100, 1, 100, 110) // 有效冷却 10
	fail(105)               // 105 < 100+100，未过静默期
	check(105, 2, 105, 125) // 有效冷却 20，max(110,125)=125
	fail(130)
	check(130, 3, 130, 170) // 有效冷却 40，max(125,170)=170
	fail(140)
	check(140, 4, 140, 200) // 有效冷却 min(60,80)=60，max(170,200)=200

	if err := s.Success("R3", 3, 150); err != nil {
		t.Fatal(err)
	}
	// Success 清 f，不改冷却截止与 lf。
	check(150, 0, 140, 200)

	fail(160)               // 160 < 140+100，未过静默期，f 从 0 加一
	check(160, 1, 160, 200) // 有效冷却 10，max(200,170)=200，不缩短

	fail(300)               // lf=160，300 >= 160+100，先清零再加一
	check(300, 1, 300, 310) // 有效冷却 10，max(200,310)=310
}

// Failure 取 max 而不缩短：后续失败的有效冷却更小，截止时刻不变。
func TestFailureNeverShortensCooldown(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "A", 1, 10, 1, 1_000_000_000, 0)
	if err := s.Failure("A", 1, 1000, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.rec(t, "A", 1).cooldownUntil; got != 1000 {
		t.Fatalf("冷却截止 = %d，期望 1000", got)
	}
	// Wr 很大，f 递增；但 cooldown 变小使有效冷却更小。
	if err := s.Failure("A", 1, 1, 10); err != nil {
		t.Fatal(err)
	}
	rec := s.rec(t, "A", 1)
	if rec.failures != 2 {
		t.Fatalf("f = %d，期望 2", rec.failures)
	}
	if got := rec.cooldownUntil; got != 1000 {
		t.Fatalf("冷却截止被缩短为 %d，期望仍为 1000", got)
	}
}

// 有效冷却封顶 CoolCap，指数移位封顶 30。
func TestCooldownCapAndShiftCap(t *testing.T) {
	s := mustNew(t, 10, 60, 1_000_000_000)
	mustAdd(t, s, "A", 1, 10, 1, 1_000_000_000, 0)
	// cooldown=1，连续失败使 f-1 超过 30：2^30 已达 1073741824 > 60，封顶 60。
	for i := 0; i < 35; i++ {
		if err := s.Failure("A", 1, 1, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	rec := s.rec(t, "A", 1)
	if rec.failures != 35 {
		t.Fatalf("f = %d，期望 35", rec.failures)
	}
	// 第 35 次失败：有效冷却 min(60, 1<<30) = 60，截止 34+60=94。
	if got := rec.cooldownUntil; got != 94 {
		t.Fatalf("冷却截止 = %d，期望 94", got)
	}

	// 指数 30 封顶：CoolCap 最大 1e9 < 2^30，故移位封顶与 CoolCap 共同保证
	// 不溢出。用 cooldown=1e9、连续 40 次失败验证：有效冷却恒为 1e9。
	s2 := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s2, "A", 1, 10, 1, 1_000_000_000, 0)
	for i := 0; i < 40; i++ {
		if err := s2.Failure("A", 1, 1_000_000_000, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// 第 40 次失败：f-1=39 > 30，有效冷却 min(1e9, 1e9<<30) = 1e9，截止 39+1e9。
	if got := s2.rec(t, "A", 1).cooldownUntil; got != 39+1_000_000_000 {
		t.Fatalf("冷却截止 = %d，期望 %d", got, 39+1_000_000_000)
	}
}

// 静默期边界：now 恰等于 lf+Wr 时清零，差 1 不清零。
func TestSilencePeriodBoundary(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 100)
	mustAdd(t, s, "A", 1, 10, 1, 1_000_000_000, 0)
	fail := func(now int64) {
		t.Helper()
		if err := s.Failure("A", 1, 10, now); err != nil {
			t.Fatal(err)
		}
	}
	fail(0)
	fail(99) // 99 < 0+100，不清零
	if f := s.rec(t, "A", 1).failures; f != 2 {
		t.Fatalf("now=99 时 f = %d，期望 2（差 1 不清零）", f)
	}
	fail(199) // lf=99，199 恰等于 99+100，清零后加一
	if f := s.rec(t, "A", 1).failures; f != 1 {
		t.Fatalf("now=199 时 f = %d，期望 1（恰等于 lf+Wr 清零）", f)
	}
	fail(298) // lf=199，298 < 299，差 1 不清零
	if f := s.rec(t, "A", 1).failures; f != 2 {
		t.Fatalf("now=298 时 f = %d，期望 2（差 1 不清零）", f)
	}
	fail(398) // lf=298，398 恰等于 298+100，清零后加一
	if f := s.rec(t, "A", 1).failures; f != 1 {
		t.Fatalf("now=398 时 f = %d，期望 1（恰等于 lf+Wr 清零）", f)
	}
}

// Success 清 f 但不缩短冷却、不改 lf；后续失败在静默期内从 0 重新计数。
func TestSuccessSemantics(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 100)
	mustAdd(t, s, "A", 1, 10, 1, 1_000_000_000, 0)
	if err := s.Failure("A", 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Failure("A", 1, 10, 10); err != nil {
		t.Fatal(err)
	}
	rec := s.rec(t, "A", 1)
	if rec.failures != 2 || rec.cooldownUntil != 30 { // max(10, 10+20)=30
		t.Fatalf("状态 (f=%d, until=%d)，期望 (2, 30)", rec.failures, rec.cooldownUntil)
	}
	if err := s.Success("A", 1, 20); err != nil {
		t.Fatal(err)
	}
	if rec.failures != 0 {
		t.Fatalf("Success 后 f = %d，期望 0", rec.failures)
	}
	if rec.cooldownUntil != 30 || rec.lastFail != 10 || !rec.hasLastFail {
		t.Fatal("Success 不应改冷却截止与 lf")
	}
	// 冷却在 30 恢复，Success 不缩短。
	mustPick(t, s, 0, 30, "A", 1)
	if _, _, err := s.Pick(0, 29); err == nil {
		t.Fatal("now=29 时期望无可用记录")
	}
	// lf=10，50 < 110 未过静默期，f 从 0 加一。
	if err := s.Failure("A", 1, 10, 50); err != nil {
		t.Fatal(err)
	}
	if rec.failures != 1 {
		t.Fatalf("f = %d，期望 1", rec.failures)
	}
}

// 题目淘汰例：Cap=2，新标识遇到期记录先淘汰再入库且序号为新号；仍满报已满。
func TestCapacityEvictionExample(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "X", 1, 10, 1, 5, 0)   // 序号 1，到期 5
	mustAdd(t, s, "Y", 2, 10, 1, 100, 1) // 序号 2，到期 101
	mustAdd(t, s, "Z", 3, 10, 1, 100, 5) // X 到期（5<=5）被淘汰，Z 入库
	if got := s.rec(t, "Z", 3).seq; got != 3 {
		t.Fatalf("Z 的序号 = %d，期望 3", got)
	}
	if len(s.records) != 2 {
		t.Fatalf("记录数 = %d，期望 2", len(s.records))
	}
	// 时刻 6 Add W：Y、Z 均未到期，已满。
	err := s.Add("W", 4, 10, 1, 100, 6)
	checkErrCode(t, err, ErrFull)
	if len(s.records) != 2 {
		t.Fatal("已满拒绝不得改变记录")
	}
	if _, ok := s.records[key{"Y", 2}]; !ok {
		t.Fatal("已满拒绝不得发生淘汰，Y 应仍在")
	}
}

// 已满时更新已有记录仍允许且不触发淘汰。
func TestUpdateWhenFull(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "X", 1, 10, 1, 5, 0)
	mustAdd(t, s, "Y", 2, 10, 1, 100, 1)
	// 时刻 10：X 已到期，但更新已有标识不需要名额也不触发淘汰。
	mustAdd(t, s, "Y", 2, 20, 7, 50, 10)
	if _, ok := s.records[key{"X", 1}]; !ok {
		t.Fatal("更新已有标识不应触发淘汰，X 应仍在")
	}
	rec := s.rec(t, "Y", 2)
	if rec.priority != 20 || rec.weight != 7 || rec.expiresAt != 60 || rec.seq != 2 {
		t.Fatal("更新未生效或序号变化")
	}
}

// Purge 后重登记获得新序号。
func TestPurgeThenReAddGetsNewSeq(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "A", 1, 10, 1, 10, 0)
	mustAdd(t, s, "B", 2, 10, 1, 10, 0)
	if s.rec(t, "A", 1).seq != 1 || s.rec(t, "B", 2).seq != 2 {
		t.Fatal("初始序号应为 1、2")
	}
	n, err := s.Purge(10)
	if err != nil || n != 2 {
		t.Fatalf("Purge(10) = (%d, %v)，期望 (2, nil)", n, err)
	}
	mustAdd(t, s, "A", 1, 10, 1, 10, 20)
	if got := s.rec(t, "A", 1).seq; got != 3 {
		t.Fatalf("重登记序号 = %d，期望 3", got)
	}
}

// 构造参数非法整体拒绝。
func TestNewInvalidConfig(t *testing.T) {
	for _, cfg := range []struct {
		cap         int
		coolCap, wr int64
	}{
		{0, 1, 1}, {-1, 1, 1},
		{1, 0, 1}, {1, 1_000_000_001, 1},
		{1, 1, 0}, {1, 1, 1_000_000_001},
	} {
		if _, err := New(cfg.cap, cfg.coolCap, cfg.wr); err == nil {
			t.Fatalf("New(%d, %d, %d) 期望被拒绝", cfg.cap, cfg.coolCap, cfg.wr)
		} else {
			checkErrCode(t, err, ErrInvalidParam)
		}
	}
}

// 各操作的拒绝原因顺序与"被拒绝不改状态"。
func TestRejectionOrderAndNoSideEffect(t *testing.T) {
	s := mustNew(t, 1, 60, 100)
	mustAdd(t, s, "A", 1, 10, 5, 100, 0)

	// Add：参数非法优先于时间非法。
	checkErrCode(t, s.Add("", 1, 10, 5, 100, -1), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 0, 10, 5, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 65536, 10, 5, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, -1, 5, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, 65536, 5, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, 10, -1, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, 10, 65536, 100, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, 10, 5, 0, 0), ErrInvalidParam)
	checkErrCode(t, s.Add("B", 2, 10, 5, 1_000_000_001, 0), ErrInvalidParam)
	// Add：时间非法优先于已满。
	checkErrCode(t, s.Add("B", 2, 10, 5, 100, -1), ErrInvalidTime)
	checkErrCode(t, s.Add("B", 2, 10, 5, 100, 1_000_000_000_000_001), ErrInvalidTime)
	// Add：已满。
	checkErrCode(t, s.Add("B", 2, 10, 5, 100, 0), ErrFull)

	// Failure：参数非法 > 时间非法 > 不存在 > 已到期。
	checkErrCode(t, s.Failure("", 1, 10, 0), ErrInvalidParam)
	checkErrCode(t, s.Failure("A", 1, 0, 0), ErrInvalidParam)
	checkErrCode(t, s.Failure("A", 1, 1_000_000_001, 0), ErrInvalidParam)
	checkErrCode(t, s.Failure("A", 1, 10, -1), ErrInvalidTime)
	checkErrCode(t, s.Failure("C", 9, 10, 0), ErrNotFound)
	checkErrCode(t, s.Failure("A", 1, 10, 100), ErrExpired) // 到期时刻 100

	// Success：参数非法 > 时间非法 > 不存在 > 已到期。
	checkErrCode(t, s.Success("", 1, 0), ErrInvalidParam)
	checkErrCode(t, s.Success("A", 1, -1), ErrInvalidTime)
	checkErrCode(t, s.Success("C", 9, 0), ErrNotFound)
	checkErrCode(t, s.Success("A", 1, 100), ErrExpired)

	// Pick / Purge：先检查时间非法。
	if _, _, err := s.Pick(0, -1); err != nil {
		checkErrCode(t, err, ErrInvalidTime)
	} else {
		t.Fatal("Pick(now=-1) 期望时间非法")
	}
	if _, err := s.Purge(1_000_000_000_000_001); err != nil {
		checkErrCode(t, err, ErrInvalidTime)
	} else {
		t.Fatal("Purge 期望时间非法")
	}

	// 上述拒绝不得改变任何记录状态。
	rec := s.rec(t, "A", 1)
	if rec.priority != 10 || rec.weight != 5 || rec.expiresAt != 100 ||
		rec.seq != 1 || rec.cooldownUntil != 0 || rec.failures != 0 || rec.hasLastFail {
		t.Fatal("被拒绝的操作改变了记录状态")
	}
	if len(s.records) != 1 {
		t.Fatal("被拒绝的操作改变了记录集合")
	}
}

// 并发调用：结果等价于某个串行顺序，且记录数不超过 Cap。
func TestConcurrentAccess(t *testing.T) {
	s := mustNew(t, 8, 60, 10)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(i)
				target := string(rune('a' + (g+i)%8))
				port := 1 + (g+i)%8
				_ = s.Add(target, port, 10, i%3, 50, now)
				_ = s.Failure(target, port, 5, now)
				_ = s.Success(target, port, now)
				_, _, _ = s.Pick(uint64(i*7+g), now)
				_, _ = s.Purge(now)
			}
		}(g)
	}
	wg.Wait()
	if len(s.records) > 8 {
		t.Fatalf("记录数 %d 超过 Cap", len(s.records))
	}
}

// 同一状态、同一 (r, now) 的 Pick 结果确定。
func TestPickDeterministic(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "A", 1, 10, 3, 1000, 0)
	mustAdd(t, s, "B", 2, 10, 7, 1000, 0)
	for i := 0; i < 100; i++ {
		mustPick(t, s, 12345, 0, "A", 1) // 12345 mod 11 = 3，累计 3>=3 → A
		mustPick(t, s, 4, 0, "B", 2)     // r1=4，累计 3<4、10>=4 → B
	}
}
