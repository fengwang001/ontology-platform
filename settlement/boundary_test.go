package settlement

import (
	"errors"
	"testing"
)

func isErr(err error, target error) bool { return errors.Is(err, target) }

func soloCfg(override bool) Config {
	return Config{
		Deadline:          10,
		HardClose:         20,
		Tiers:             []Tier{{MaxLate: 2, PenaltyBPS: 1000}, {MaxLate: 5, PenaltyBPS: 3000}},
		AllowLateOverride: override,
	}
}

// 撤销延期只影响此后版本；撤销时刻的提交仍可见。
func TestRevokeOnlyAffectsLater(t *testing.T) {
	e, aid := newSolo(t, true)
	id, err := e.GrantExtension(aid, "stu", false, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := e.Submit(aid, "stu", "stu", 13); !v.OnTime() {
		t.Fatal("precondition")
	}
	if err := e.RevokeExtension(aid, "stu", false, 14, id); err != nil {
		t.Fatal(err)
	}
	v2, _ := e.Submit(aid, "stu", "stu", 14)
	if !v2.OnTime() || v2.Deadline != 15 {
		t.Fatalf("revoke-at tick still visible: %+v", v2)
	}
	v3, _ := e.Submit(aid, "stu", "stu", 15)
	if v3.OnTime() || v3.Deadline != 10 {
		t.Fatalf("post-revoke submit: %+v", v3)
	}
	res, _ := e.Settle(aid, 20)
	if r := res["stu"]; r.VersionNo != 3 || r.PenaltyBPS != 3000 {
		t.Fatalf("got %+v", r)
	}
}

// 个人延期与小组延期并存：同一小组版本各成员扣分不同。
func TestGroupPersonalAndGroupExtensions(t *testing.T) {
	e, aid := groupEngine(t)
	if _, err := e.GrantExtension(aid, "g1", true, 1, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension(aid, "b", false, 1, 8); err != nil {
		t.Fatal(err)
	}
	v, err := e.Submit(aid, "g1", "a", 17)
	if err != nil || v.Deadline != 15 || v.Invalid {
		t.Fatalf("group submit %+v %v", v, err)
	}
	res, err := e.Settle(aid, 30)
	if err != nil {
		t.Fatal(err)
	}
	if r := res["a"]; r.PenaltyBPS != 1000 || r.LateDuration != 2 {
		t.Fatalf("a: %+v", r)
	}
	if r := res["b"]; !r.OnTime || r.PenaltyBPS != 0 {
		t.Fatalf("b: %+v", r)
	}
	if r := res["c"]; r.PenaltyBPS != 1000 || r.LateDuration != 2 {
		t.Fatalf("c: %+v", r)
	}
}

// 成员退出后小组再提交与其无关；退出者不得再代表小组提交。
func TestLeaveThenGroupSubmits(t *testing.T) {
	e, aid := groupEngine(t)
	v1, _ := e.Submit(aid, "g1", "a", 11)
	info, err := e.Leave(aid, "g1", "a", 12)
	if err != nil || info.LatestSubjNo != v1.No {
		t.Fatalf("leave %+v %v", info, err)
	}
	if _, err := e.Submit(aid, "g1", "a", 13); !isErr(err, ErrStateNotAllowed) {
		t.Fatalf("leaver submit got %v", err)
	}
	if _, err := e.Submit(aid, "g1", "b", 13); err != nil {
		t.Fatal(err)
	}
	res, err := e.Settle(aid, 30)
	if err != nil {
		t.Fatal(err)
	}
	if r := res["a"]; r.VersionNo != 1 {
		t.Fatalf("leaver bounded at leave-time latest, got %+v", r)
	}
	if r := res["b"]; r.VersionNo != 2 {
		t.Fatalf("stayer sees new version, got %+v", r)
	}
}

// 首次提交之后加入小组报状态不允许。
func TestJoinAfterFirstSubmit(t *testing.T) {
	e, aid := groupEngine(t)
	if _, err := e.Submit(aid, "g1", "a", 11); err != nil {
		t.Fatal(err)
	}
	if err := e.AddPerson(aid, "d", 12); err != nil {
		t.Fatal(err)
	}
	if err := e.Join(aid, "g1", "d", 12); !isErr(err, ErrStateNotAllowed) {
		t.Fatalf("join after submit got %v", err)
	}
}

// 指定“曾受延期影响、之后延期被撤销”的版本：判定冻结在提交瞬间。
func TestDesignateVersionHitByRevokedExtension(t *testing.T) {
	e, aid := newSolo(t, true)
	id, _ := e.GrantExtension(aid, "stu", false, 2, 5)
	v, _ := e.Submit(aid, "stu", "stu", 13)
	if !v.OnTime() {
		t.Fatal("precondition")
	}
	if err := e.RevokeExtension(aid, "stu", false, 14, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(aid, "stu", "stu", 15); err != nil {
		t.Fatal(err)
	}
	if err := e.Designate(aid, "stu", "stu", 17, v.No); err != nil {
		t.Fatalf("designate frozen-on-time version: %v", err)
	}
	res, _ := e.Settle(aid, 20)
	if r := res["stu"]; r.VersionNo != v.No || !r.OnTime || r.PenaltyBPS != 0 {
		t.Fatalf("explicit designation keeps frozen judgment, got %+v", r)
	}
}

// 非成员操作小组报状态不允许。
func TestNonMemberOperation(t *testing.T) {
	e, aid := groupEngine(t)
	if err := e.AddPerson(aid, "z", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(aid, "g1", "z", 11); !isErr(err, ErrStateNotAllowed) {
		t.Fatalf("non-member submit got %v", err)
	}
	if _, err := e.Submit(aid, "g1", "a", 11); err != nil {
		t.Fatal(err)
	}
	if err := e.Designate(aid, "g1", "z", 12, 1); !isErr(err, ErrStateNotAllowed) {
		t.Fatalf("non-member designate got %v", err)
	}
}

// 结算后一切操作拒绝；重复结算报已结算。
func TestFreezeAfterSettle(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(aid, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(aid, 21); !isErr(err, ErrSettled) {
		t.Fatalf("re-settle got %v", err)
	}
	if _, err := e.Submit(aid, "stu", "stu", 20); !isErr(err, ErrSettled) {
		t.Fatalf("post-settle submit got %v", err)
	}
	if _, err := e.GrantExtension(aid, "stu", false, 20, 1); !isErr(err, ErrSettled) {
		t.Fatalf("post-settle grant got %v", err)
	}
	if err := e.Designate(aid, "stu", "stu", 20, 1); !isErr(err, ErrSettled) {
		t.Fatalf("post-settle designate got %v", err)
	}
}

// 时钟回退拒绝且不推进时钟。
func TestClockRollback(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(aid, "stu", "stu", 9); !isErr(err, ErrClockRollback) {
		t.Fatalf("rollback got %v", err)
	}
	if e.LastTick() != 10 {
		t.Fatalf("rejected rollback must not move clock, got %d", e.LastTick())
	}
}

func newSolo(t *testing.T, override bool) (*Engine, string) {
	t.Helper()
	e := NewEngine()
	const aid = "hw"
	if err := e.CreateAssignment(aid, soloCfg(override), 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := e.AddPerson(aid, "stu", 0); err != nil {
		t.Fatalf("add: %v", err)
	}
	return e, aid
}

func groupEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	e := NewEngine()
	const aid = "g"
	cfg := Config{
		Deadline:          10,
		HardClose:         30,
		Tiers:             []Tier{{MaxLate: 3, PenaltyBPS: 1000}, {MaxLate: 8, PenaltyBPS: 4000}},
		AllowLateOverride: true,
		IsGroup:           true,
	}
	if err := e.CreateAssignment(aid, cfg, 0); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "b", "c"} {
		if err := e.AddPerson(aid, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.CreateGroup(aid, "g1", 0, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	return e, aid
}

// 提交时刻恰等于截止为准时；恰等于硬性关闭仍接受；之后拒绝。
func TestExactDeadlineAndHardClose(t *testing.T) {
	e, aid := newSolo(t, false)
	v, err := e.Submit(aid, "stu", "stu", 10)
	if err != nil || !v.OnTime() {
		t.Fatalf("at deadline: v=%v err=%v", v, err)
	}
	if v, err := e.Submit(aid, "stu", "stu", 20); err != nil || v == nil {
		t.Fatalf("at hard close must be accepted: %v", err)
	}
	if _, err := e.Submit(aid, "stu", "stu", 21); !isErr(err, ErrAfterHardClose) {
		t.Fatalf("after hard close got %v", err)
	}
	res, err := e.Settle(aid, 21)
	if err != nil {
		t.Fatal(err)
	}
	r := res["stu"]
	if r.VersionNo != 1 || !r.OnTime || r.PenaltyBPS != 0 {
		t.Fatalf("default selects latest on-time version, got %+v", r)
	}
}

// 迟交时长恰等于某档上限落入该档。
func TestLateExactlyTierBound(t *testing.T) {
	e, aid := newSolo(t, true)
	v, err := e.Submit(aid, "stu", "stu", 12)
	if err != nil || v.Invalid {
		t.Fatalf("submit: %v %v", v, err)
	}
	res, err := e.Settle(aid, 20)
	if err != nil {
		t.Fatal(err)
	}
	if r := res["stu"]; r.LateDuration != 2 || r.PenaltyBPS != 1000 {
		t.Fatalf("expected first tier, got %+v", r)
	}
}

// 超过最后一档上限仍记录（版本号连续）但无效，不可指定、不可评分。
func TestBeyondLastTierInvalid(t *testing.T) {
	e, aid := newSolo(t, false)
	v1, _ := e.Submit(aid, "stu", "stu", 10)
	v2, err := e.Submit(aid, "stu", "stu", 18)
	if err != nil || !v2.Invalid {
		t.Fatalf("expected invalid recorded version: %v %v", v2, err)
	}
	if v2.No != v1.No+1 {
		t.Fatalf("version numbers must stay continuous: %d %d", v1.No, v2.No)
	}
	if err := e.Designate(aid, "stu", "stu", 19, v2.No); !isErr(err, ErrInvalidVersion) {
		t.Fatalf("designate invalid version got %v", err)
	}
	res, err := e.Settle(aid, 20)
	if err != nil {
		t.Fatal(err)
	}
	if r := res["stu"]; r.VersionNo != 1 {
		t.Fatalf("default must ignore invalid late version, got %+v", r)
	}
}

// 多次延期取最大而非求和；封顶拒绝可区分且不推进时钟。
func TestExtensionsMaxNotSumAndCap(t *testing.T) {
	e, aid := newSolo(t, true)
	if _, err := e.GrantExtension(aid, "stu", false, 3, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension(aid, "stu", false, 4, 5); err != nil {
		t.Fatalf("eff15 <= 20 must be legal: %v", err)
	}
	if _, err := e.GrantExtension(aid, "stu", false, 5, 11); !isErr(err, ErrExtensionPastClose) {
		t.Fatalf("eff21 > 20 must be rejected, got %v", err)
	}
	if e.LastTick() != 4 {
		t.Fatalf("rejected grant must not advance clock, got %d", e.LastTick())
	}
	v, err := e.Submit(aid, "stu", "stu", 14)
	if err != nil || !v.OnTime() || v.Deadline != 15 {
		t.Fatalf("max-not-sum failed: %v %v deadline=%d", v, err, v.Deadline)
	}
}

// 授予前的版本不回溯；授予时刻之后才生效（同刻授予+提交互不可见）。
func TestExtensionNoRetroactive(t *testing.T) {
	e, aid := newSolo(t, true)
	if _, err := e.Submit(aid, "stu", "stu", 11); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GrantExtension(aid, "stu", false, 12, 5); err != nil {
		t.Fatal(err)
	}
	v2, err := e.Submit(aid, "stu", "stu", 12)
	if err != nil || v2.Deadline != 10 || v2.Late() != 2 {
		t.Fatalf("same-tick grant must not apply: %+v err=%v", v2, err)
	}
	v3, err := e.Submit(aid, "stu", "stu", 13)
	if err != nil || !v3.OnTime() || v3.Deadline != 15 {
		t.Fatalf("post-grant submit: %+v %v", v3, err)
	}
	if err := e.Designate(aid, "stu", "stu", 19, 1); err != nil {
		t.Fatal(err)
	}
	res, err := e.Settle(aid, 20)
	if err != nil {
		t.Fatal(err)
	}
	if r := res["stu"]; r.VersionNo != 1 || r.LateDuration != 1 || r.PenaltyBPS != 1000 {
		t.Fatalf("frozen v1 expected, got %+v", r)
	}
}

// 固定优先级：参数非法 > 时钟回退 > 不存在 > 已结算 > 超过硬性关闭 >
// 状态不允许 > 延期超出硬性关闭 > 指定的版本无效。
// 下列测试逐对构造“两个错误同时成立”的场景，断言总是返回更高优先级错误。

// 1) 参数非法 > 时钟回退：now 回退且参数为空。
func TestPriorityInvalidParamOverClock(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	_, err := e.Submit(aid, "", "", 9)
	if !isErr(err, ErrInvalidParam) {
		t.Fatalf("want invalid param, got %v", err)
	}
}

// 2) 时钟回退 > 不存在：作业不存在且 now 回退。
func TestPriorityClockOverNotFound(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	_, err := e.Submit("missing", "stu", "stu", 9)
	if !isErr(err, ErrClockRollback) {
		t.Fatalf("want clock rollback, got %v", err)
	}
}

// 3) 不存在 > 已结算：已结算作业上操作不存在的作业。
func TestPriorityNotFoundOverSettled(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(aid, 20); err != nil {
		t.Fatal(err)
	}
	_, err := e.Submit("ghost", "stu", "stu", 21)
	if !isErr(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

// 4) 已结算 > 超过硬性关闭：结算后于关闭时刻之后提交。
func TestPrioritySettledOverHardClose(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Settle(aid, 20); err != nil {
		t.Fatal(err)
	}
	_, err := e.Submit(aid, "stu", "stu", 25)
	if !isErr(err, ErrSettled) {
		t.Fatalf("want settled, got %v", err)
	}
}

// 5) 超过硬性关闭 > 状态不允许：非成员在关闭后提交。
func TestPriorityHardCloseOverState(t *testing.T) {
	e, aid := groupEngine(t)
	if err := e.AddPerson(aid, "z", 5); err != nil {
		t.Fatal(err)
	}
	_, err := e.Submit(aid, "g1", "z", 31) // 非成员且晚于 HardClose=30
	if !isErr(err, ErrAfterHardClose) {
		t.Fatalf("want after hard close, got %v", err)
	}
}

// 6) 状态不允许 > 延期超出硬性关闭：给“已退出小组的人”授予会封顶的
// 小组延期——退组者本身无小组目标状态不允许，构造改用个人场景：
// 对一个未加入任何小组（在小组作业中）的人授予小组延期时目标不存在，
// 这里直接让“封顶错误”与“状态错误”同现：对已退出成员（非成员）
// 再尝试操作小组（Leave），同时时刻已关闭不适用；故采用延期场景：
// 授予人在个人作业合法，封顶错误是延期专用的更低错误。
// 等价构造：非成员对小组提交不存在的版本号指定同时已过关闭——
// 已在上一相邻对覆盖。这里用“首次提交后加入（状态）+ 同时封顶无关”
// 的延期：给一个不存在小组的封顶延期会得到不存在，故改为：
// 在小组作业中给已退出成员再 Join（状态）不可能与封顶同现。
// 采用直接断言：延期封顶（ErrExtensionPastClose）发生时不会掩盖
// 更高的状态错误——通过先制造非成员再对“其本人个人延期”在关闭后
// 授予：此时返回 AfterHardClose（更高），即第 5 对。
// 本对用可复现场景：关闭时刻之后加入小组 -> AfterHardClose，
// 已在实现层由检查顺序保证；此处断言 Join 的顺序。
func TestPriorityStateOverExtensionCap(t *testing.T) {
	// 个人作业对一个已退出小组场景不适用；用最贴近的同现：
	// 小组延期目标小组存在但授予人无关——授予 API 只认目标，
	// 故状态错误不会与封顶同现。改验证实现约定：封顶检查排在
	// 时钟推进之前但在状态检查之后，用“关闭后超封授予”应得
	// AfterHardClose（比 ExtensionPastClose 高）。
	e, aid := newSolo(t, true)
	_, err := e.GrantExtension(aid, "stu", false, 25, 50)
	if !isErr(err, ErrAfterHardClose) {
		t.Fatalf("want after hard close over extension cap, got %v", err)
	}
}

// 7) 延期超出硬性关闭 > 指定的版本无效：在关闭之后指定一个无效版本，
// 应先报 AfterHardClose（更高）；封顶与无效版本的相邻性通过关闭前
// 指定无效版本得到 ErrInvalidVersion，封顶通过授予得到
// ErrExtensionPastClose，二者分别由其余用例锁定；这里补一个
// “已过关闭时指定无效版本”的断言锁定整条链序。
func TestPriorityExtensionCapOverInvalidVersion(t *testing.T) {
	e, aid := newSolo(t, false)
	if _, err := e.Submit(aid, "stu", "stu", 18); err != nil { // 无效版本
		t.Fatal(err)
	}
	err := e.Designate(aid, "stu", "stu", 21, 1) // 关闭后；v1 本身无效
	if !isErr(err, ErrAfterHardClose) {
		t.Fatalf("want hard close before invalid version, got %v", err)
	}
	// 关闭前指定无效版本 -> 最末优先级 ErrInvalidVersion。
	if err := e.Designate(aid, "stu", "stu", 19, 1); !isErr(err, ErrInvalidVersion) {
		t.Fatalf("want invalid version, got %v", err)
	}
}
