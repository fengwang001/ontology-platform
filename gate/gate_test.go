package gate

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		MinRows:     100,
		MaxNullRate: Rational{Num: 1, Den: 10}, // 10%
		BaselineK:   3,
		WarnLoPct:   80,
		WarnHiPct:   120,
		MaxBlocksM:  100,
	}
}

func newTestGate(t *testing.T) *Gate {
	t.Helper()
	g, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func mustSubmit(t *testing.T, g *Gate, seq, rows, nulls int64) Decision {
	t.Helper()
	d, err := g.Submit(seq, rows, nulls)
	if err != nil {
		t.Fatalf("submit %d: %v", seq, err)
	}
	return d
}

func hasRule(rs []RuleID, want RuleID) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

func TestRulesBlockAndAllow(t *testing.T) {
	g := newTestGate(t)

	// 规则一：行数低于下限 -> 阻断
	d, err := g.Submit(1, 50, 0)
	if err != nil || d.Allowed || d.State != StateQuarantined {
		t.Fatalf("rule1: d=%+v err=%v", d, err)
	}

	// 规则二：20/100 > 1/10（恰等 10% 不超限）-> 阻断
	d, err = g.Submit(2, 100, 20)
	if err != nil || d.Allowed || !hasRule(d.Violations, RuleNullRate) {
		t.Fatalf("rule2: d=%+v err=%v", d, err)
	}

	// 行数为 0 不评估规则二，只触发规则一
	d, err = g.Submit(3, 0, 0)
	if err != nil || d.Allowed || !hasRule(d.Violations, RuleMinRows) || hasRule(d.Violations, RuleNullRate) {
		t.Fatalf("rows0: d=%+v err=%v", d, err)
	}

	// 恰等空值率边界 10% -> 不超限，放行
	d, err = g.Submit(4, 100, 10)
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("boundary nullrate: d=%+v err=%v", d, err)
	}
}

func TestViolationsOrderedAndCombined(t *testing.T) {
	g := newTestGate(t)
	// 同时违反规则一与规则二：按规则次序列出 [1,2]，阻断
	d, err := g.Submit(1, 10, 5)
	if err != nil || d.Allowed || len(d.Violations) != 2 {
		t.Fatalf("combined: d=%+v err=%v", d, err)
	}
	if d.Violations[0] != RuleMinRows || d.Violations[1] != RuleNullRate {
		t.Fatalf("order: %v", d.Violations)
	}
}

func TestBaselineBySeqNotArrival(t *testing.T) {
	g := newTestGate(t)

	// 乱序到达：先 seq=3，再 seq=1、seq=2
	mustSubmit(t, g, 3, 200, 0)
	mustSubmit(t, g, 1, 200, 0)
	mustSubmit(t, g, 2, 200, 0)

	// seq=4 基线为序号更小的 {1,2,3}，与到达次序无关；中位数 200。
	d, err := g.Submit(4, 240, 0) // 恰等上界 120% -> 不告警
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("boundary equal high: d=%+v err=%v", d, err)
	}

	d, err = g.Submit(5, 159, 0) // 159 < 160 严格越界 -> 告警放行
	if err != nil || !d.Allowed || !d.Warning || !hasRule(d.Violations, RuleBaseline) {
		t.Fatalf("warn low: d=%+v err=%v", d, err)
	}

	d, err = g.Submit(6, 160, 0) // 恰等下界 80% -> 不告警
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("boundary equal low: d=%+v err=%v", d, err)
	}
}

func TestBaselineEvenLowerMedian(t *testing.T) {
	cfg := testConfig()
	cfg.BaselineK = 10
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// 两个基线行数 200、400，升序 [200,400]，下标 (2-1)/2=0 -> 下中位数 200。
	mustSubmit(t, g, 1, 200, 0)
	mustSubmit(t, g, 2, 400, 0)

	// 241 对下中位数 200 越界（>240），对上中位数 400 不会越界。
	d, err := g.Submit(3, 241, 0)
	if err != nil || !d.Allowed || !d.Warning {
		t.Fatalf("lower median expected warning: d=%+v err=%v", d, err)
	}
	d, err = g.Submit(4, 240, 0) // 恰等下中位数上界
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("lower median boundary: d=%+v err=%v", d, err)
	}

	// 四个基线 [200,400,600,800]，下标 (4-1)/2=1 -> 下中位数 400。
	mustSubmit(t, g, 5, 600, 0)
	mustSubmit(t, g, 6, 800, 0)
	d, err = g.Submit(7, 481, 0) // 481 > 400*120%=480
	if err != nil || !d.Allowed || !d.Warning {
		t.Fatalf("four baseline lower median: d=%+v err=%v", d, err)
	}
}

func TestBaselineKMostRecent(t *testing.T) {
	cfg := testConfig()
	cfg.BaselineK = 2
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 序号最小的旧批次行数 2000；K=2 时 seq=4 只能取 {2,3}（各 200）。
	mustSubmit(t, g, 1, 2000, 0)
	mustSubmit(t, g, 2, 200, 0)
	mustSubmit(t, g, 3, 200, 0)

	d, err := g.Submit(4, 250, 0) // 对中位数 200 越界(>240)，对 2000 不越界
	if err != nil || !d.Allowed || !d.Warning {
		t.Fatalf("K window: d=%+v err=%v", d, err)
	}
}

func TestNoBaselineNoRuleThree(t *testing.T) {
	g := newTestGate(t)
	// 没有任何已放行批次：规则三不评估，超大行数也不告警。
	d, err := g.Submit(1, 500, 0)
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("no baseline: d=%+v err=%v", d, err)
	}
	// 隔离区批次不作为基线：seq=2 阻断后，seq=3 仍无基线。
	g.Submit(2, 1, 0)
	d, err = g.Submit(3, 500, 0)
	if err != nil || !d.Allowed || d.Warning {
		t.Fatalf("quarantined not baseline: d=%+v err=%v", d, err)
	}
}

func TestManualReleaseEntersBaseline(t *testing.T) {
	g := newTestGate(t)

	d, err := g.Submit(1, 50, 0) // 阻断进隔离
	if err != nil || d.Allowed {
		t.Fatalf("setup: %+v %v", d, err)
	}
	mustSubmit(t, g, 2, 100, 0) // seq=2 无基线，正常放行

	if err := g.ManuallyRelease(1); err != nil { // 行数 50 进入基线
		t.Fatalf("manual release: %v", err)
	}
	if st, ok := g.Query(1); !ok || st != StateManuallyReleased {
		t.Fatalf("state=%v ok=%v", st, ok)
	}

	// seq=3 行数 100（不触发规则一），基线 {1:50,2:100} 下中位数 50，上界 60。
	// 100 > 60 告警，证明人工放行批次已成为基线。
	d, err = g.Submit(3, 100, 0)
	if err != nil || !d.Allowed || !d.Warning {
		t.Fatalf("manual baseline: d=%+v err=%v", d, err)
	}
}

func TestQuarantineResubmit(t *testing.T) {
	g := newTestGate(t)

	g.Submit(1, 50, 0) // 隔离

	d, err := g.Resubmit(1, 100, 0) // 同序号新数据，合规放行
	if err != nil || !d.Allowed || d.State != StateReleased {
		t.Fatalf("resubmit allow: d=%+v err=%v", d, err)
	}
	if _, err := g.Resubmit(1, 100, 0); !errors.Is(err, ErrAlreadyRel) {
		t.Fatalf("resubmit after release want ErrAlreadyRel, got %v", err)
	}
	if err := g.ManuallyRelease(1); !errors.Is(err, ErrAlreadyRel) {
		t.Fatalf("release after release want ErrAlreadyRel, got %v", err)
	}
}

func TestDiscardLocksSeq(t *testing.T) {
	g := newTestGate(t)
	g.Submit(1, 50, 0)

	if err := g.Discard(1); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := g.Resubmit(1, 100, 0); !errors.Is(err, ErrAlreadyDisc) {
		t.Fatalf("resubmit after discard: %v", err)
	}
	if err := g.ManuallyRelease(1); !errors.Is(err, ErrAlreadyDisc) {
		t.Fatalf("release after discard: %v", err)
	}
	if err := g.Discard(1); !errors.Is(err, ErrAlreadyDisc) {
		t.Fatalf("double discard: %v", err)
	}
	if _, err := g.Submit(1, 100, 0); !errors.Is(err, ErrSeqExists) {
		t.Fatalf("submit discarded seq: %v", err)
	}
}

func TestRejectionReasons(t *testing.T) {
	g := newTestGate(t)

	if _, err := g.Submit(0, 100, 0); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("seq<=0: %v", err)
	}
	if _, err := g.Submit(1, -1, 0); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("rows<0: %v", err)
	}
	if _, err := g.Submit(1, 10, -1); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("null<0: %v", err)
	}
	if _, err := g.Submit(1, 10, 11); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("null>rows: %v", err)
	}

	mustSubmit(t, g, 1, 100, 0)
	if _, err := g.Submit(1, 100, 0); !errors.Is(err, ErrSeqExists) {
		t.Fatalf("dup submit: %v", err)
	}

	if _, err := g.Resubmit(99, 100, 0); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("resubmit unknown: %v", err)
	}
	if err := g.ManuallyRelease(99); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("release unknown: %v", err)
	}
	if err := g.Discard(99); !errors.Is(err, ErrSeqNotFound) {
		t.Fatalf("discard unknown: %v", err)
	}
	if err := g.Discard(1); !errors.Is(err, ErrAlreadyRel) {
		t.Fatalf("discard released: %v", err)
	}
}

func TestPauseAndResume(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocksM = 3
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	g.Submit(1, 50, 0)
	g.Submit(2, 50, 0)
	d, err := g.Submit(3, 50, 0) // 第三次连续阻断 -> 暂停
	if err != nil || d.Allowed {
		t.Fatalf("third block: d=%+v err=%v", d, err)
	}
	if snap := g.Snapshot(); !snap.Paused || snap.ConsecutiveBlocked != 3 {
		t.Fatalf("snap=%+v", snap)
	}

	// 暂停期间提交/重投一律拒绝，且不裁决、不落任何状态
	if _, err := g.Submit(4, 100, 0); !errors.Is(err, ErrPaused) {
		t.Fatalf("submit while paused: %v", err)
	}
	if _, err := g.Resubmit(1, 100, 0); !errors.Is(err, ErrPaused) {
		t.Fatalf("resubmit while paused: %v", err)
	}
	if _, ok := g.Query(4); ok {
		t.Fatal("rejected submit must not register seq")
	}

	// 人工放行不受暂停影响，且不清零阻断计数
	if err := g.ManuallyRelease(1); err != nil {
		t.Fatalf("manual release while paused: %v", err)
	}
	if snap := g.Snapshot(); !snap.Paused || snap.ConsecutiveBlocked != 3 {
		t.Fatalf("manual release must not touch blocks: %+v", snap)
	}
	// 丢弃同样不受影响
	if err := g.Discard(2); err != nil {
		t.Fatalf("discard while paused: %v", err)
	}

	g.Resume()
	if snap := g.Snapshot(); snap.Paused || snap.ConsecutiveBlocked != 0 {
		t.Fatalf("after resume: %+v", snap)
	}
	d, err = g.Submit(4, 50, 0) // 恢复后重新从 0 计
	if err != nil || d.Allowed {
		t.Fatalf("after resume blocks restart: d=%+v err=%v", d, err)
	}
	if snap := g.Snapshot(); snap.ConsecutiveBlocked != 1 {
		t.Fatalf("expected blocks=1, got %d", snap.ConsecutiveBlocked)
	}
}

func TestWarningAndDiscardDoNotMisCount(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocksM = 3
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, g, 1, 200, 0) // 建立基线（中位数 200）
	g.Submit(2, 50, 0)          // blocks=1
	g.Submit(3, 50, 0)          // blocks=2
	// 告警放行（500 > 200*120%，但满足阻断规则）使计数清零，不触发 M=3 暂停
	mustSubmit(t, g, 4, 500, 0)
	if snap := g.Snapshot(); snap.Paused || snap.ConsecutiveBlocked != 0 {
		t.Fatalf("warning release resets: %+v", snap)
	}

	// 丢弃不计入也不清零：阻断两次后丢弃一个隔离批次，计数仍是 2。
	g.Submit(5, 50, 0) // blocks=1
	g.Submit(6, 50, 0) // blocks=2
	g.Submit(7, 50, 0) // blocks=3 -> 暂停
	g.Resume()
	g.Submit(8, 50, 0) // blocks=1
	g.Submit(9, 50, 0) // blocks=2
	if err := g.Discard(8); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if snap := g.Snapshot(); snap.Paused || snap.ConsecutiveBlocked != 2 {
		t.Fatalf("discard must not touch blocks: %+v", snap)
	}
}

func TestResubmitBlockCountsTowardPause(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocksM = 3
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.Submit(1, 50, 0) // blocks=1
	g.Submit(2, 50, 0) // blocks=2
	// 对 seq=1 重投仍然违规 -> 这是新的一次裁决，blocks=3 -> 暂停
	if _, err := g.Resubmit(1, 50, 0); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if snap := g.Snapshot(); !snap.Paused || snap.ConsecutiveBlocked != 3 {
		t.Fatalf("resubmit block counts: %+v", snap)
	}
}

func TestConcurrentNoLostUpdateAndNoDecisionWhilePaused(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocksM = 1000 // 放高阈值，先验证计数不丢失更新
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	const n = 200
	var wg sync.WaitGroup
	for i := int64(1); i <= n; i++ {
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			_, _ = g.Submit(seq, 50, 0) // 全部阻断
		}(i)
	}
	wg.Wait()
	if snap := g.Snapshot(); snap.ConsecutiveBlocked != n {
		t.Fatalf("lost update: blocks=%d want %d", snap.ConsecutiveBlocked, n)
	}

	// M=1 的门禁：并发提交下，达到 1 即暂停，之后没有任何提交被裁决。
	cfg2 := testConfig()
	cfg2.MaxBlocksM = 1
	g2, _ := New(cfg2)
	const m = 100
	var wg2 sync.WaitGroup
	var mu sync.Mutex
	quarantined := 0
	rejected := 0
	for i := int64(1); i <= m; i++ {
		wg2.Add(1)
		go func(seq int64) {
			defer wg2.Done()
			d, err := g2.Submit(seq, 50, 0)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && !d.Allowed:
				quarantined++
			case errors.Is(err, ErrPaused):
				rejected++
			}
		}(i)
	}
	wg2.Wait()
	if quarantined != 1 || quarantined+rejected != m {
		t.Fatalf("quarantined=%d rejected=%d (sum must be %d)", quarantined, rejected, m)
	}
	snap := g2.Snapshot()
	if !snap.Paused || len(snap.KnownSeqs) != 1 {
		t.Fatalf("exactly one decision must land: %+v", snap)
	}
}

func TestConcurrentMixedOps(t *testing.T) {
	g := newTestGate(t)
	var wg sync.WaitGroup
	// 多序号并行混合提交/查询/恢复/隔离操作，仅要求竞态检测下不死锁、不崩溃。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for k := int64(0); k < 50; k++ {
				seq := base*1000 + k + 1
				rows := int64(100)
				if k%3 == 0 {
					rows = 1
				}
				_, _ = g.Submit(seq, rows, 0)
				_, _ = g.Query(seq)
				_ = g.ManuallyRelease(seq)
				_ = g.Discard(seq)
				if k%10 == 0 {
					g.Resume()
				}
			}
		}(int64(w))
	}
	wg.Wait()
}

func TestLoggingInputOutputAndBasis(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlocksM = 3
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	g.SetLogger(&buf)

	mustSubmit(t, g, 1, 100, 0)
	d, err := g.Submit(2, 50, 20) // 违反规则一+规则二
	if err != nil || d.Allowed {
		t.Fatalf("setup: %+v %v", d, err)
	}
	logs := buf.String()
	for _, want := range []string{
		"SUBMIT seq=2 rows=50 null=20", // 输入
		"BLOCK",                        // 输出
		"violations=[1 2 3]",           // 判定依据（seq=1 基线使规则三也命中）
		"blocks=1",                     // 判定依据：seq=1 放行清零，seq=2 为首次阻断
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q in:\n%s", want, logs)
		}
	}

	buf.Reset()
	if _, err := g.Resubmit(2, 100, 0); err != nil { // 重投放行
		t.Fatal(err)
	}
	logs = buf.String()
	if !strings.Contains(logs, "RESUBMIT") || !strings.Contains(logs, "ALLOW") {
		t.Fatalf("resubmit log missing basis:\n%s", logs)
	}

	buf.Reset()
	g.Submit(3, 1, 0)
	g.Submit(4, 1, 0)
	g.Submit(5, 1, 0) // 连续阻断 3 次 -> 暂停
	if _, err := g.Submit(500, 1, 0); !errors.Is(err, ErrPaused) {
		t.Fatalf("expected paused rejection, got %v", err)
	}
	if !strings.Contains(buf.String(), "reason=channel_paused") {
		t.Fatalf("rejection reason not logged:\n%s", buf.String())
	}
}
