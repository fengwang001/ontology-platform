package ontology

import (
	"math/bits"
	"sort"
	"sync"
	"testing"
)

func indConfig() AssignmentConfig {
	return AssignmentConfig{
		ID: "ind", Deadline: 100, HardClose: 200,
		Tiers: []Tier{{MaxLate: 10, Penalty: 0.1}, {MaxLate: 20, Penalty: 0.25}, {MaxLate: 50, Penalty: 0.5}},
	}
}

func grpConfig() AssignmentConfig {
	return AssignmentConfig{
		ID: "grp", Deadline: 100, HardClose: 200, GroupWork: true, AllowLateOverride: true,
		Tiers: []Tier{{MaxLate: 10, Penalty: 0.1}, {MaxLate: 20, Penalty: 0.25}, {MaxLate: 50, Penalty: 0.5}},
	}
}

func mustCreate(t *testing.T, e *Engine, cfg AssignmentConfig, now int64) {
	t.Helper()
	if err := e.CreateAssignment(cfg, now); err != nil {
		t.Fatalf("create %s: %v", cfg.ID, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, aid, subject, member string, now int64) int {
	t.Helper()
	v, err := e.Submit(aid, subject, member, now)
	if err != nil {
		t.Fatalf("submit %s/%s@%d: %v", aid, subject, now, err)
	}
	return v
}

func mustSettle(t *testing.T, e *Engine, aid string, now int64) map[string]MemberSettlement {
	t.Helper()
	res, err := e.Settle(aid, now)
	if err != nil {
		t.Fatalf("settle %s: %v", aid, err)
	}
	out := map[string]MemberSettlement{}
	for _, ms := range res {
		out[ms.MemberID] = ms
	}
	return out
}

func wantErrKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	k, ok := ErrKindOf(err)
	if !ok || k != kind {
		t.Fatalf("want error kind %v, got %v", kind, err)
	}
}

// 提交时刻恰等于截止时刻为准时。
func TestSubmitExactlyAtDeadline(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	v := mustSubmit(t, e, "ind", "s1", "s1", 100)
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 1 || ms.Late != 0 || ms.Penalty != 0 || ms.Invalid {
		t.Fatalf("settlement = %+v, want on-time v1", ms)
	}
}

// 提交时刻恰等于硬性关闭时刻仍被接受；晚一刻则拒绝且可区分。
func TestSubmitExactlyAtHardClose(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	v := mustSubmit(t, e, "ind", "s1", "s1", 200)
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	if _, err := e.Submit("ind", "s1", "s1", 201); true {
		wantErrKind(t, err, ErrClosed)
	}
}

// 迟交时长恰等于某档上限时落入该档。
func TestLateExactlyAtTierBoundary(t *testing.T) {
	e := NewEngine()
	cfg := indConfig()
	cfg.AllowLateOverride = true // 允许迟交版本被选为评分版本
	mustCreate(t, e, cfg, 0)
	mustSubmit(t, e, "ind", "s1", "s1", 110) // late=10，恰为第 1 档上限
	mustSubmit(t, e, "ind", "s2", "s2", 111) // late=11，落入第 2 档
	got := mustSettle(t, e, "ind", 201)
	if ms := got["s1"]; ms.Penalty != 0.1 || ms.Late != 10 || ms.Invalid {
		t.Fatalf("s1 = %+v, want penalty 0.1", ms)
	}
	if ms := got["s2"]; ms.Penalty != 0.25 || ms.Late != 11 || ms.Invalid {
		t.Fatalf("s2 = %+v, want penalty 0.25", ms)
	}
}

// 超过最后一档上限：版本仍记录但无效，不得被选为评分版本，显式指定被拒绝且可区分。
func TestBeyondLastTierInvalid(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	v := mustSubmit(t, e, "ind", "s1", "s1", 151) // late=51 > 50
	if v != 1 {
		t.Fatalf("invalid version still recorded, got v=%d", v)
	}
	wantErrKind(t, e.DesignateVersion("ind", "s1", "s1", 1, 152), ErrVersionInvalid)
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 0 || !ms.Invalid {
		t.Fatalf("settlement = %+v, want no grading version and invalid", ms)
	}
}

// 多次延期取最大延长时长而非求和。
func TestExtensionsMaxNotSum(t *testing.T) {
	e := NewEngine()
	cfg := indConfig()
	cfg.AllowLateOverride = true
	mustCreate(t, e, cfg, 0)
	if _, err := e.GrantExtension("ind", PersonalTarget, "s1", 10, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension("ind", PersonalTarget, "s1", 6, 6); err != nil {
		t.Fatal(err)
	}
	eff, err := e.EffectiveDeadline("ind", "s1")
	if err != nil || eff != 110 {
		t.Fatalf("effective deadline = %d, %v; want 110 (max, not sum 116)", eff, err)
	}
	// 若为求和则 116 时刻准时；取最大则 late=6，扣 0.1。
	mustSubmit(t, e, "ind", "s1", "s1", 116)
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Late != 6 || ms.Penalty != 0.1 {
		t.Fatalf("settlement = %+v, want late=6 penalty 0.1", ms)
	}
}

// 延期在授予时刻之后才生效：授予前的版本判定不回溯。
func TestExtensionNotRetroactive(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	mustSubmit(t, e, "ind", "s1", "s1", 120) // 无延期：late=20 -> 0.25
	if _, err := e.GrantExtension("ind", PersonalTarget, "s1", 30, 121); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "ind", "s1", "s1", 125) // 有延期：eff=130，准时
	// 显式指定 v1，结算须保留其提交时刻的判定（late=20, 0.25）。
	if err := e.DesignateVersion("ind", "s1", "s1", 1, 126); err != nil {
		t.Fatal(err)
	}
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 1 || ms.Late != 20 || ms.Penalty != 0.25 {
		t.Fatalf("settlement = %+v, want v1 late=20 penalty 0.25", ms)
	}
}

// 撤销延期只影响此后的版本。
func TestRevokeExtension(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	gid, err := e.GrantExtension("ind", PersonalTarget, "s1", 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "ind", "s1", "s1", 120) // eff=130，准时
	if err := e.RevokeExtension("ind", gid, 121); err != nil {
		t.Fatal(err)
	}
	eff, _ := e.EffectiveDeadline("ind", "s1")
	if eff != 100 {
		t.Fatalf("effective deadline after revoke = %d, want 100", eff)
	}
	mustSubmit(t, e, "ind", "s1", "s1", 122) // 撤销后：late=22 -> 0.5
	ms := mustSettle(t, e, "ind", 201)["s1"]
	// 默认取准时版本中最新者：v1（撤销不回溯）。
	if ms.Version != 1 || ms.Penalty != 0 {
		t.Fatalf("settlement = %+v, want v1 on-time", ms)
	}
}

// 显式指定被撤销延期影响过的版本：结算仍按提交时刻快照。
func TestDesignateVersionAffectedByRevokedExtension(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	gid, _ := e.GrantExtension("ind", PersonalTarget, "s1", 30, 5)
	mustSubmit(t, e, "ind", "s1", "s1", 120) // eff=130 -> 准时
	if err := e.RevokeExtension("ind", gid, 121); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "ind", "s1", "s1", 122) // late=22 -> 0.5
	if err := e.DesignateVersion("ind", "s1", "s1", 1, 123); err != nil {
		t.Fatal(err)
	}
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 1 || ms.Late != -10 || ms.Penalty != 0 {
		t.Fatalf("settlement = %+v, want v1 judged with extension (late=-10)", ms)
	}
}

// 小组成员个人延期与小组延期并存：扣分按成员分别结算。
func TestGroupPersonalAndGroupExtensions(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, grpConfig(), 0)
	if err := e.JoinGroup("grp", "g1", "m1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.JoinGroup("grp", "g1", "m2", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension("grp", PersonalTarget, "m1", 30, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension("grp", GroupTarget, "g1", 10, 4); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "grp", "g1", "m1", 120)
	got := mustSettle(t, e, "grp", 201)
	// m1：eff = 100 + max(30,10) = 130 -> 准时；m2：eff = 100+10 -> late=10, 0.1。
	if ms := got["m1"]; ms.Version != 1 || ms.Late != -10 || ms.Penalty != 0 {
		t.Fatalf("m1 = %+v, want on-time", ms)
	}
	if ms := got["m2"]; ms.Version != 1 || ms.Late != 10 || ms.Penalty != 0.1 {
		t.Fatalf("m2 = %+v, want late=10 penalty 0.1", ms)
	}
}

// 成员退出后小组再提交：退出者结算以退出时刻最新版本为准。
func TestMemberLeavesThenGroupSubmits(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, grpConfig(), 0)
	_ = e.JoinGroup("grp", "g1", "m1", 1)
	_ = e.JoinGroup("grp", "g1", "m2", 2)
	if _, err := e.GrantExtension("grp", GroupTarget, "g1", 10, 3); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "grp", "g1", "m1", 120) // v1: 主体 eff=110, late=10
	if err := e.LeaveGroup("grp", "g1", "m2", 130); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, e, "grp", "g1", "m1", 140) // v2: m2 已无快照
	got := mustSettle(t, e, "grp", 201)
	// m1 按默认规则取最新有效版本 v2（late=30 -> 0.5）。
	if ms := got["m1"]; ms.Version != 2 || ms.Late != 30 || ms.Penalty != 0.5 {
		t.Fatalf("m1 = %+v, want v2 late=30 penalty 0.5", ms)
	}
	// m2 冻结在退出时刻的 v1。
	if ms := got["m2"]; ms.Version != 1 || ms.Late != 10 || ms.Penalty != 0.1 {
		t.Fatalf("m2 = %+v, want frozen v1 late=10 penalty 0.1", ms)
	}
}

// 允许迟交覆盖时取全部有效版本中最新者；否则取准时版本中最新者。
func TestLateOverrideConfig(t *testing.T) {
	cfg := indConfig()
	cfg.AllowLateOverride = true
	e := NewEngine()
	mustCreate(t, e, cfg, 0)
	mustSubmit(t, e, "ind", "s1", "s1", 100) // 准时
	mustSubmit(t, e, "ind", "s1", "s1", 110) // late=10 -> 0.1
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 2 || ms.Penalty != 0.1 {
		t.Fatalf("override: %+v, want v2 penalty 0.1", ms)
	}

	e2 := NewEngine()
	mustCreate(t, e2, indConfig(), 0)
	mustSubmit(t, e2, "ind", "s1", "s1", 100)
	mustSubmit(t, e2, "ind", "s1", "s1", 110)
	ms2 := mustSettle(t, e2, "ind", 201)["s1"]
	if ms2.Version != 1 || ms2.Penalty != 0 {
		t.Fatalf("no override: %+v, want v1 on-time", ms2)
	}
}

// 显式指定优先于默认规则。
func TestExplicitDesignateBeatsDefault(t *testing.T) {
	cfg := indConfig()
	cfg.AllowLateOverride = true
	e := NewEngine()
	mustCreate(t, e, cfg, 0)
	mustSubmit(t, e, "ind", "s1", "s1", 100)
	mustSubmit(t, e, "ind", "s1", "s1", 110)
	if err := e.DesignateVersion("ind", "s1", "s1", 1, 120); err != nil {
		t.Fatal(err)
	}
	ms := mustSettle(t, e, "ind", 201)["s1"]
	if ms.Version != 1 || ms.Penalty != 0 {
		t.Fatalf("settlement = %+v, want designated v1", ms)
	}
}

// 首次提交之后加入小组、非成员操作小组：状态不允许。
func TestGroupStateNotAllowed(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, grpConfig(), 0)
	mustCreate(t, e, indConfig(), 1)
	_ = e.JoinGroup("grp", "g1", "m1", 2)
	mustSubmit(t, e, "grp", "g1", "m1", 10)
	wantErrKind(t, e.JoinGroup("grp", "g1", "m2", 11), ErrStateNotAllowed)
	_, err := e.Submit("grp", "g1", "m3", 12)
	wantErrKind(t, err, ErrStateNotAllowed)
	wantErrKind(t, e.JoinGroup("ind", "g2", "m2", 13), ErrStateNotAllowed)
	_, err = e.Submit("ind", "s1", "s2", 14)
	wantErrKind(t, err, ErrStateNotAllowed)
	wantErrKind(t, e.LeaveGroup("grp", "g1", "m3", 15), ErrStateNotAllowed)
}

// 结算后全部状态冻结；重复结算报已结算。
func TestSettleFreezes(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, grpConfig(), 0)
	_ = e.JoinGroup("grp", "g1", "m1", 1)
	mustSubmit(t, e, "grp", "g1", "m1", 10)
	if _, err := e.Settle("grp", 201); err != nil {
		t.Fatal(err)
	}
	_, err := e.Settle("grp", 202)
	wantErrKind(t, err, ErrAlreadySettled)
	_, err = e.Submit("grp", "g1", "m1", 202)
	wantErrKind(t, err, ErrAlreadySettled)
	_, err = e.GrantExtension("grp", GroupTarget, "g1", 5, 202)
	wantErrKind(t, err, ErrAlreadySettled)
	wantErrKind(t, e.DesignateVersion("grp", "g1", "m1", 1, 202), ErrAlreadySettled)
	wantErrKind(t, e.JoinGroup("grp", "g2", "m2", 202), ErrAlreadySettled)
	wantErrKind(t, e.LeaveGroup("grp", "g1", "m1", 202), ErrAlreadySettled)
}

// 拒绝优先级逐对验证：构造同时命中两类错误的操作，断言报告高优先级者。
func TestErrorPriorityPairs(t *testing.T) {
	newEngine := func(t *testing.T) *Engine {
		e := NewEngine()
		mustCreate(t, e, indConfig(), 0)
		mustCreate(t, e, grpConfig(), 1)
		if err := e.JoinGroup("grp", "g1", "m1", 2); err != nil {
			t.Fatal(err)
		}
		mustSubmit(t, e, "ind", "s1", "s1", 10) // 时钟推进到 10
		return e
	}
	t.Run("InvalidArgument>ClockRegression", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.Submit("", "", "", 5) // 空参数 + 时钟回退
		wantErrKind(t, err, ErrInvalidArgument)
	})
	t.Run("ClockRegression>NotFound", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.Submit("nope", "s1", "s1", 5) // 回退 + 作业不存在
		wantErrKind(t, err, ErrClockRegression)
	})
	t.Run("NotFound>AlreadySettled", func(t *testing.T) {
		e := newEngine(t)
		mustSettle(t, e, "ind", 201)
		_, err := e.Submit("nope", "s1", "s1", 202) // 不存在 + 目标作业已结算的语境
		wantErrKind(t, err, ErrNotFound)
	})
	t.Run("AlreadySettled>Closed", func(t *testing.T) {
		e := newEngine(t)
		mustSettle(t, e, "ind", 201)
		_, err := e.Submit("ind", "s1", "s1", 202) // 已结算 + 已关闭
		wantErrKind(t, err, ErrAlreadySettled)
	})
	t.Run("Closed>StateNotAllowed", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.Submit("grp", "g1", "m9", 201) // 已关闭 + 非成员
		wantErrKind(t, err, ErrClosed)
	})
	t.Run("StateNotAllowed>VersionInvalid", func(t *testing.T) {
		e := newEngine(t)
		mustSubmit(t, e, "grp", "g1", "m1", 151)             // 主体级无效版本（late=51）
		err := e.DesignateVersion("grp", "g1", "m9", 1, 152) // 非成员 + 版本无效
		wantErrKind(t, err, ErrStateNotAllowed)
	})
	t.Run("Closed>VersionInvalid", func(t *testing.T) {
		e := newEngine(t)
		mustSubmit(t, e, "ind", "s1", "s1", 151)             // 无效版本
		err := e.DesignateVersion("ind", "s1", "s1", 1, 202) // 已关闭 + 版本无效
		wantErrKind(t, err, ErrClosed)
	})
	t.Run("AlreadySettled>ExtensionExceedsClose", func(t *testing.T) {
		e := newEngine(t)
		mustSettle(t, e, "ind", 201)
		_, err := e.GrantExtension("ind", PersonalTarget, "s1", 500, 202) // 已结算 + 延期超界
		wantErrKind(t, err, ErrAlreadySettled)
	})
	t.Run("ClockRegression>ExtensionExceedsClose", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.GrantExtension("ind", PersonalTarget, "s1", 500, 5) // 回退 + 超界
		wantErrKind(t, err, ErrClockRegression)
	})
	t.Run("NotFound>VersionInvalid", func(t *testing.T) {
		e := newEngine(t)
		err := e.DesignateVersion("ind", "ghost", "ghost", 1, 20) // 主体不存在
		wantErrKind(t, err, ErrNotFound)
	})
	t.Run("ExtensionExceedsClose", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.GrantExtension("ind", PersonalTarget, "s1", 101, 20) // 100+101>200
		wantErrKind(t, err, ErrExtensionExceedsClose)
		if _, err := e.GrantExtension("ind", PersonalTarget, "s1", 100, 21); err != nil {
			t.Fatalf("100+100<=200 should be accepted: %v", err)
		}
	})
}

// 被拒绝的操作不推进时钟。
func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	mustSubmit(t, e, "ind", "s1", "s1", 10)               // 时钟 = 10
	if _, err := e.Submit("ind", "s1", "s1", 300); true { // 已关闭，拒绝
		wantErrKind(t, err, ErrClosed)
	}
	// 若时钟被推进到 300，则 t=20 的授予会因回退被拒；实际应被接受。
	if _, err := e.GrantExtension("ind", PersonalTarget, "s1", 5, 20); err != nil {
		t.Fatalf("clock must stay at 10 after rejection: %v", err)
	}
}

// 被拒绝的提交不消耗版本号：每个主体版本号连续无洞。
func TestVersionNumbersContiguous(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	if v := mustSubmit(t, e, "ind", "s1", "s1", 10); v != 1 {
		t.Fatalf("v=%d, want 1", v)
	}
	if _, err := e.Submit("ind", "s1", "s1", 5); true { // 时钟回退，拒绝
		wantErrKind(t, err, ErrClockRegression)
	}
	if _, err := e.Submit("ind", "s1", "s1", 300); true { // 已关闭，拒绝
		wantErrKind(t, err, ErrClosed)
	}
	if v := mustSubmit(t, e, "ind", "s1", "s1", 20); v != 2 {
		t.Fatalf("v=%d, want 2 (rejections must not consume numbers)", v)
	}
}

// 并发提交等价于某个串行顺序：版本号恰好为 1..N 各一次。
func TestConcurrentSubmits(t *testing.T) {
	e := NewEngine()
	mustCreate(t, e, indConfig(), 0)
	const n = 64
	versions := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := e.Submit("ind", "s1", "s1", 50)
			if err != nil {
				t.Error(err)
				return
			}
			versions[i] = v
		}(i)
	}
	wg.Wait()
	sort.Ints(versions)
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("versions = %v, want contiguous 1..%d", versions, n)
		}
	}
}

// 档位查找为二分：比较次数不超过 bits.Len(n)，即只随档位数对数增长。
func TestTierLookupComparisonBound(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7, 8, 100, 1000, 1 << 16} {
		tiers := make([]Tier, n)
		for i := range tiers {
			tiers[i] = Tier{MaxLate: int64(i + 1), Penalty: float64(i+1) / float64(n+1)}
		}
		bound := bits.Len(uint(n))
		for _, late := range []int64{1, int64(n) / 2, int64(n), int64(n) + 1} {
			tier, ok, cmp := findTierCounted(tiers, late)
			if cmp > bound {
				t.Fatalf("n=%d late=%d: %d comparisons > bound %d", n, late, cmp, bound)
			}
			wantOk := late <= int64(n)
			if ok != wantOk {
				t.Fatalf("n=%d late=%d: ok=%v, want %v", n, late, ok, wantOk)
			}
			if ok && tier.MaxLate < late {
				t.Fatalf("n=%d late=%d: tier %+v does not cover", n, late, tier)
			}
			if ok && tier.MaxLate != late && late > 1 {
				// 首个覆盖档：前一档必不覆盖（本构造中档位连续，覆盖档上限必等于 late）。
				prev := tiers[tier.MaxLate-2]
				if prev.MaxLate >= late {
					t.Fatalf("n=%d late=%d: not the first covering tier", n, late)
				}
			}
		}
	}
}

// 有效截止查询不随延期总数增长：懒删除堆的弹出总数不超过授权总数，
// 且陈旧元素清空后的每次查询弹出 0 个元素。
func TestExtensionQueryIsConstant(t *testing.T) {
	s := newExtSet()
	const n = 100000
	for i := 0; i < n; i++ {
		s.add(int64(i%97 + 1))
	}
	// 撤销约一半授权，制造大量陈旧堆元素。
	for d := int64(1); d <= 97; d += 2 {
		for s.counts[d] > 0 {
			s.remove(d)
		}
	}
	want := int64(96) // 剩余偶数时长中的最大值
	got, pops1 := s.maxProbed()
	if got != want {
		t.Fatalf("max = %d, want %d", got, want)
	}
	if pops1 > n {
		t.Fatalf("pops %d exceed total grants %d", pops1, n)
	}
	for i := 0; i < 1000; i++ {
		got, pops := s.maxProbed()
		if got != want || pops != 0 {
			t.Fatalf("steady-state query: max=%d pops=%d, want %d/0", got, pops, want)
		}
	}
}
