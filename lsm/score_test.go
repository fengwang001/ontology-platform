package lsm

import (
	"testing"
)

// TestScoreExactlyOne 分数恰等于一的层必须被选中。
func TestScoreExactlyOne(t *testing.T) {
	// 零层：文件数 == 触发阈值，分数恰为 1。
	s := newTestService(t, testCfg())
	for i := uint64(1); i <= 4; i++ {
		mustAdd(t, s, mf(i, 0, byte(i), byte(i), 10))
	}
	p := mustPick(t, s)
	if p.Level != 0 {
		t.Fatalf("expected L0 plan, got L%d", p.Level)
	}
	if p.Score != "1" {
		t.Fatalf("expected score 1, got %s", p.Score)
	}

	// 非零层：总字节 == 目标字节，分数恰为 1。
	s2 := newTestService(t, testCfg())
	mustAdd(t, s2, mf(1, 1, 10, 20, 1000))
	p2 := mustPick(t, s2)
	if p2.Level != 1 || p2.Score != "1" {
		t.Fatalf("expected L1 plan with score 1, got L%d score %s", p2.Level, p2.Score)
	}
}

// TestScoreJustBelowOne 分数略小于一的层不得被选中，计划为空是正常结果。
func TestScoreJustBelowOne(t *testing.T) {
	// 零层：3/4 < 1。
	s := newTestService(t, testCfg())
	for i := uint64(1); i <= 3; i++ {
		mustAdd(t, s, mf(i, 0, byte(i), byte(i), 10))
	}
	p, err := s.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil plan for score 3/4, got %v", p)
	}

	// 非零层：999/1000 < 1。
	s2 := newTestService(t, testCfg())
	mustAdd(t, s2, mf(1, 1, 10, 20, 999))
	p2, err := s2.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if p2 != nil {
		t.Fatalf("expected nil plan for score 999/1000, got %v", p2)
	}
}

// TestScoreTiePrefersLowerLevel 分数并列时取层号较小者。
func TestScoreTiePrefersLowerLevel(t *testing.T) {
	// L1 与 L2 分数都是 2：L1=2000/1000，L2=20000/10000。
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 19, 2000))
	mustAdd(t, s, mf(2, 2, 30, 39, 20000))
	p := mustPick(t, s)
	if p.Level != 1 {
		t.Fatalf("tie should prefer lower level, got L%d", p.Level)
	}
}

// TestScoreExactRationalNoFloatRounding 分数比较不得因浮点舍入改变结果。
// L1 分数恰为 1，L2 分数为 1+2^-53；float64 会把后者舍入成 1.0，
// 从而误判为并列并选择层号较小的 L1；精确有理数必须选出 L2。
func TestScoreExactRationalNoFloatRounding(t *testing.T) {
	const base = int64(1) << 53 // 9007199254740992
	cfg := Config{NumLevels: 4, L0Trigger: 4, BaseLevelBytes: base, LevelMultiplier: 1}
	s := newTestService(t, cfg)
	mustAdd(t, s, mf(1, 1, 10, 19, base))
	mustAdd(t, s, mf(2, 2, 30, 39, base+1))
	p := mustPick(t, s)
	if p.Level != 2 {
		t.Fatalf("exact rational comparison must pick L2 (score 1+2^-53 > 1), got L%d", p.Level)
	}
}

// TestLevelTargetGrowth 目标字节按固定倍数逐层增长。
func TestLevelTargetGrowth(t *testing.T) {
	cfg := testCfg()
	if got := cfg.levelTargetBytes(1).String(); got != "1000" {
		t.Fatalf("L1 target = %s, want 1000", got)
	}
	if got := cfg.levelTargetBytes(2).String(); got != "10000" {
		t.Fatalf("L2 target = %s, want 10000", got)
	}
	if got := cfg.levelTargetBytes(3).String(); got != "100000" {
		t.Fatalf("L3 target = %s, want 100000", got)
	}
}
