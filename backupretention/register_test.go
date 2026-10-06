package backupretention

import "testing"

// 2024-01-01 00:00 UTC 是周一。
const mon20240101 int64 = 1704067200
const daySeconds int64 = 86400

func TestRegisterFullAndIncremental(t *testing.T) {
	s := NewService()
	mustOK(t, s.RegisterFull("F", 100, 10))
	// 父子时刻相等合法。
	mustOK(t, s.RegisterIncremental("I", 50, 10, "F"))
	mustOK(t, s.RegisterIncremental("I2", 50, 20, "I"))

	wantErr(t, s.RegisterFull("F", 1, 30), KindDuplicateID)

	s2 := NewService()
	mustOK(t, s2.RegisterFull("G", 1, 100))
	wantErr(t, s2.RegisterIncremental("I3", 1, 100, "nope"), KindParentNotFound)
	// 时序矛盾的独立判定：白盒插入一个“创建时刻晚于时钟基线”的父备份，
	// 验证次序：参数 → 时钟（通过）→ 重复（无）→ 父存在 → 子早于父。
	s2.reg.add(&Backup{ID: "future", Kind: KindFull, CreatedAt: 200})
	wantErr(t, s2.RegisterIncremental("I4", 1, 100, "future"), KindTimeContradiction)

	wantErr(t, s.RegisterIncremental("I5", 1, 30, ""), KindInvalidParam)
}

func TestInvalidParams(t *testing.T) {
	s := NewService()
	wantErr(t, s.RegisterFull("", 1, 0), KindInvalidParam)
	wantErr(t, s.RegisterFull("A", -1, 0), KindInvalidParam)
	wantErr(t, s.RegisterFull("A", 1, -1), KindInvalidParam)
	wantErr(t, s.RegisterFull("A", MaxBackupSize+1, 0), KindInvalidParam)
	wantErr(t, s.RegisterFull("A", 1, MaxTimestamp+1), KindInvalidParam)
	wantErr(t, s.MarkCorrupt("A", -1), KindInvalidParam)
	_, err := s.Plan(-1)
	wantErr(t, err, KindInvalidParam)

	// 边界 0 与 1000 合法；-1 与 1001 越界。
	mustOK(t, s.SetPolicy(Policy{Daily: 0, Weekly: 1000, Monthly: 1000}, 0))
	wantErr(t, s.SetPolicy(Policy{Daily: -1}, 1), KindLayerOutOfRange)
	wantErr(t, s.SetPolicy(Policy{Monthly: 1001}, 1), KindLayerOutOfRange)
}

func TestErrorPrecedence(t *testing.T) {
	s := NewService()
	mustOK(t, s.RegisterFull("F", 1, 100))

	// 参数非法最优先。
	wantErr(t, s.RegisterFull("", 1, 0), KindInvalidParam)
	// 参数合法但时钟回退且标识重复：时钟优先于重复。
	wantErr(t, s.RegisterFull("F", 1, 50), KindClockSkew)
	// 增量：时钟回退优先于父不存在/时序矛盾。
	wantErr(t, s.RegisterIncremental("X", 1, 10, "nope"), KindClockSkew)
	// 时刻合法后，标识重复优先于父不存在。
	wantErr(t, s.RegisterIncremental("F", 1, 200, "nope"), KindDuplicateID)
	// SetPolicy：时钟回退优先于层数量越界。
	wantErr(t, s.SetPolicy(Policy{Daily: 1 << 30}, 50), KindClockSkew)
	// 时钟合法后才判定层数量越界。
	wantErr(t, s.SetPolicy(Policy{Daily: 1 << 30}, 200), KindLayerOutOfRange)
	// 时刻本身非法仍最优先报参数非法。
	wantErr(t, s.SetPolicy(Policy{Daily: 1 << 30}, -1), KindInvalidParam)
	// MarkCorrupt：空 id（参数）优先；时钟优先于备份不存在。
	wantErr(t, s.MarkCorrupt("", 200), KindInvalidParam)
	wantErr(t, s.MarkCorrupt("F", 50), KindClockSkew)
	wantErr(t, s.MarkCorrupt("ghost", 50), KindClockSkew)
	// SetLegalHold 同样：时钟优先于不存在。
	wantErr(t, s.SetLegalHold("ghost", 50, true), KindClockSkew)
}

func TestClockSkewRejectionLeavesNoTrace(t *testing.T) {
	s := NewService()
	mustOK(t, s.RegisterFull("F", 1, 100))
	wantErr(t, s.RegisterFull("X", 1, 90), KindClockSkew)
	// 拒绝未推进时钟：95 依然回退。
	wantErr(t, s.RegisterFull("Y", 1, 95), KindClockSkew)
	// 100 合法；X/Y 从未登记，故不重复。
	mustOK(t, s.RegisterFull("X", 1, 100))
	mustOK(t, s.RegisterFull("Y", 1, 100))

	// 只读 Plan 也推进时钟基线。
	var err error
	_, err = s.Plan(120)
	mustOK(t, err)
	_, err = s.Plan(119)
	wantErr(t, err, KindClockSkew)

	// 被拒绝的损坏标记不得改变状态：对已删除/不存在 id 标记后再查仍不存在。
	wantErr(t, s.MarkCorrupt("Z", 110), KindClockSkew)
}

func TestPlanAndSetPolicyAdvanceClock(t *testing.T) {
	s := NewService()
	mustOK(t, s.RegisterFull("F", 1, 10))
	mustOK(t, s.SetPolicy(Policy{Daily: 1}, 20))
	if s.lastClock != 20 {
		t.Fatalf("lastClock=%d want 20", s.lastClock)
	}
	_, err := s.Plan(30)
	mustOK(t, err)
	if s.lastClock != 30 {
		t.Fatalf("lastClock=%d want 30", s.lastClock)
	}
}
