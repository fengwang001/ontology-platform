package submission

import (
	"sync"
	"testing"
)

func stdTiers() []Tier {
	return []Tier{{MaxLate: 3, Penalty: 0.1}, {MaxLate: 7, Penalty: 0.3}, {MaxLate: 12, Penalty: 0.5}}
}

func mustCreate(t *testing.T, s *System, cfg AssignmentConfig) {
	t.Helper()
	if err := s.CreateAssignment(0, cfg); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func individualCfg() AssignmentConfig {
	return AssignmentConfig{ID: "a1", Deadline: 10, HardClose: 20, Tiers: stdTiers()}
}

func groupCfg() AssignmentConfig {
	return AssignmentConfig{ID: "g1", Deadline: 10, HardClose: 20, Tiers: stdTiers(), GroupMode: true}
}

func mustSubmit(t *testing.T, s *System, at int, aid, sid, payload string) int {
	t.Helper()
	no, err := s.Submit(at, aid, sid, payload)
	if err != nil {
		t.Fatalf("submit at=%d: %v", at, err)
	}
	return no
}

func mustCode(t *testing.T, err error, want Code) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error code %v, got %v", want, err)
	}
	if e.Code != want {
		t.Fatalf("want code %v, got %v (%v)", want, e.Code, err)
	}
}

func judgmentOf(t *testing.T, s *System, aid, subject string, no int, member string) Judgment {
	t.Helper()
	vers := s.Versions(aid, subject)
	if no < 1 || no > len(vers) {
		t.Fatalf("version %d not found", no)
	}
	j, ok := vers[no-1].Judgments[member]
	if !ok {
		t.Fatalf("no judgment for %q in version %d", member, no)
	}
	return j
}

// 提交时刻恰等于截止视为准时；恰等于硬性关闭仍被接受。
func TestSubmitExactlyAtDeadlineAndHardClose(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg())
	mustSubmit(t, s, 10, "a1", "st", "v1")
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); !j.OnTime || j.Late != 0 || j.Penalty != 0 {
		t.Fatalf("at==deadline should be on time, got %+v", j)
	}
	mustSubmit(t, s, 20, "a1", "st", "v2")
	j := judgmentOf(t, s, "a1", "st", 2, "st")
	if j.OnTime || j.Late != 10 || j.Penalty != 0.5 {
		t.Fatalf("at==hardClose should be accepted and penalized, got %+v", j)
	}
	if _, err := s.Submit(21, "a1", "st", "v3"); err == nil {
		t.Fatal("at>hardClose should be rejected")
	} else {
		mustCode(t, err, ErrHardClosed)
	}
	if got := len(s.Versions("a1", "st")); got != 2 {
		t.Fatalf("rejected submit consumed a version number, versions=%d", got)
	}
}

// 迟交时长恰等于某档上限时取该档。
func TestLateExactlyAtTierCap(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg())
	mustSubmit(t, s, 13, "a1", "st", "v1") // late=3 == 第一档上限
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); j.Penalty != 0.1 || j.Late != 3 {
		t.Fatalf("late==cap should hit that tier, got %+v", j)
	}
	mustSubmit(t, s, 14, "a1", "st", "v2") // late=4 -> 第二档
	if j := judgmentOf(t, s, "a1", "st", 2, "st"); j.Penalty != 0.3 {
		t.Fatalf("late=4 should hit second tier, got %+v", j)
	}
}

// 超过最后一档上限：版本仍记录但无效，不得被选为评分版本。
func TestExceedLastTierMakesInvalidVersion(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, AssignmentConfig{ID: "a1", Deadline: 10, HardClose: 30, Tiers: stdTiers()})
	mustSubmit(t, s, 10, "a1", "st", "on-time")
	mustSubmit(t, s, 25, "a1", "st", "too-late") // late=15 > 12，无效
	vers := s.Versions("a1", "st")
	if len(vers) != 2 {
		t.Fatalf("invalid version must still be recorded, versions=%d", len(vers))
	}
	if j := judgmentOf(t, s, "a1", "st", 2, "st"); j.Valid {
		t.Fatalf("late beyond last tier should be invalid, got %+v", j)
	}
	mustCode(t, s.SelectVersion(26, "a1", "st", 2), ErrVersionInvalid)
	sett, err := s.Settle(31, "a1")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	got := sett[0]
	if got.VersionNo != 1 || !got.Valid || got.Penalty != 0 {
		t.Fatalf("default grading version should be latest on-time v1, got %+v", got)
	}
}

// 多次延期取最大延长时长而非求和。
func TestMultipleExtensionsTakeMaxNotSum(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg()) // deadline=10, hardClose=20
	if _, err := s.GrantPersonalExtension(1, "a1", "st", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantPersonalExtension(2, "a1", "st", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantPersonalExtension(3, "a1", "st", 5); err != nil {
		t.Fatal(err)
	}
	eff, _ := s.EffectiveDeadline("a1", "st")
	if eff != 17 { // 10 + max(3,7,5)，而非 10+3+7+5=25
		t.Fatalf("effective deadline should use max, got %d", eff)
	}
	mustSubmit(t, s, 17, "a1", "st", "v1")
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); !j.OnTime {
		t.Fatalf("at==deadline+max should be on time, got %+v", j)
	}
}

// 延期授予前已产生的版本不回溯；授予后的新版本按新的有效截止判定。
func TestExtensionNotRetroactive(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg())
	mustSubmit(t, s, 15, "a1", "st", "before") // late=5 -> 0.3
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); j.Penalty != 0.3 {
		t.Fatalf("before grant: got %+v", j)
	}
	if _, err := s.GrantPersonalExtension(16, "a1", "st", 6); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, s, 16, "a1", "st", "after") // eff=16，准时
	if j := judgmentOf(t, s, "a1", "st", 2, "st"); !j.OnTime {
		t.Fatalf("after grant should be on time, got %+v", j)
	}
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); j.Penalty != 0.3 {
		t.Fatalf("old version must not be re-judged, got %+v", j)
	}
}

// 撤销延期只影响此后的版本。
func TestRevokeAffectsOnlyNewVersions(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg())
	id, err := s.GrantPersonalExtension(1, "a1", "st", 6)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, s, 16, "a1", "st", "with-ext") // eff=16，准时
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); !j.OnTime {
		t.Fatalf("with ext should be on time, got %+v", j)
	}
	if err := s.RevokeExtension(17, id); err != nil {
		t.Fatal(err)
	}
	if eff, _ := s.EffectiveDeadline("a1", "st"); eff != 10 {
		t.Fatalf("after revoke effective deadline should fall back to 10, got %d", eff)
	}
	mustSubmit(t, s, 17, "a1", "st", "after-revoke") // 按无延期判定 late=7 -> 0.3
	if j := judgmentOf(t, s, "a1", "st", 2, "st"); j.OnTime || j.Penalty != 0.3 {
		t.Fatalf("new version after revoke should be judged without ext, got %+v", j)
	}
	if j := judgmentOf(t, s, "a1", "st", 1, "st"); !j.OnTime {
		t.Fatalf("old version must keep its snapshot, got %+v", j)
	}
}

func mustJoin(t *testing.T, s *System, at int, aid, gid, sid string) {
	t.Helper()
	if err := s.JoinGroup(at, aid, gid, sid); err != nil {
		t.Fatalf("join %q->%q: %v", sid, gid, err)
	}
}

// 小组作业：成员共享提交主体；个人延期只影响本人，小组延期对全体成员生效。
func TestGroupPersonalAndGroupExtensionsCoexist(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, groupCfg()) // deadline=10, hardClose=20
	mustJoin(t, s, 1, "g1", "team", "alice")
	mustJoin(t, s, 1, "g1", "team", "bob")
	if _, err := s.GrantPersonalExtension(2, "g1", "alice", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantGroupExtension(3, "g1", "team", 3); err != nil {
		t.Fatal(err)
	}
	// alice: 10+max(5,3)=15；bob: 10+3=13
	if eff, _ := s.EffectiveDeadline("g1", "alice"); eff != 15 {
		t.Fatalf("alice eff=%d", eff)
	}
	if eff, _ := s.EffectiveDeadline("g1", "bob"); eff != 13 {
		t.Fatalf("bob eff=%d", eff)
	}
	no := mustSubmit(t, s, 14, "g1", "bob", "v1") // 任一成员的提交都是小组的版本
	if no != 1 {
		t.Fatalf("group version numbering, got %d", no)
	}
	if j := judgmentOf(t, s, "g1", "team", 1, "alice"); !j.OnTime {
		t.Fatalf("alice should be on time, got %+v", j)
	}
	if j := judgmentOf(t, s, "g1", "team", 1, "bob"); j.OnTime || j.Penalty != 0.1 {
		t.Fatalf("bob late=1 should be 0.1, got %+v", j)
	}
	sett, err := s.Settle(21, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sett) != 2 {
		t.Fatalf("want 2 settlements, got %d", len(sett))
	}
	byMember := map[string]Settlement{}
	for _, x := range sett {
		byMember[x.Member] = x
	}
	if byMember["alice"].Penalty != 0 || byMember["bob"].Penalty != 0.1 {
		t.Fatalf("asymmetric penalty: %+v", sett)
	}
}

// 成员退出后小组再提交：退出者以退出时刻最新版本结算，与新版本无关。
func TestLeaveGroupThenGroupSubmits(t *testing.T) {
	s := NewSystem()
	cfg := groupCfg()
	cfg.AllowLateOverride = true
	mustCreate(t, s, cfg)
	mustJoin(t, s, 1, "g1", "team", "alice")
	mustJoin(t, s, 1, "g1", "team", "bob")
	mustSubmit(t, s, 5, "g1", "team", "alice") // v1，准时
	if err := s.LeaveGroup(6, "g1", "alice"); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, s, 19, "g1", "team", "bob") // v2，对 bob late=9 -> 0.5
	mustCode(t, s.Submit(19, "g1", "alice", "x"), ErrStateNotAllowed)
	sett, err := s.Settle(21, "g1")
	if err != nil {
		t.Fatal(err)
	}
	byMember := map[string]Settlement{}
	for _, x := range sett {
		byMember[x.Member] = x
	}
	if byMember["alice"].VersionNo != 1 || byMember["alice"].Penalty != 0 {
		t.Fatalf("leaver should settle on leave-time latest v1, got %+v", byMember["alice"])
	}
	if byMember["bob"].VersionNo != 2 || byMember["bob"].Penalty != 0.5 {
		t.Fatalf("bob should settle on latest valid v2, got %+v", byMember["bob"])
	}
}

// 显式指定一个「因延期而有效、后延期被撤销」的版本：指定仍然有效（不回溯）。
func TestExplicitSelectVersionAffectedByRevokedExtension(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, individualCfg())
	id, err := s.GrantPersonalExtension(1, "a1", "st", 6)
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, s, 15, "a1", "st", "v1") // eff=16，准时
	mustSubmit(t, s, 16, "a1", "st", "v2") // eff=16，准时
	if err := s.RevokeExtension(17, id); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, s, 18, "a1", "st", "v3") // 无延期，late=8 -> 0.5
	if err := s.SelectVersion(19, "a1", "st", 1); err != nil {
		t.Fatalf("selecting version judged under revoked ext must stay valid: %v", err)
	}
	sett, err := s.Settle(21, "a1")
	if err != nil {
		t.Fatal(err)
	}
	got := sett[0]
	if got.VersionNo != 1 || !got.Explicit || !got.Valid || got.Penalty != 0 {
		t.Fatalf("explicit selection should win, got %+v", got)
	}
}

// 迟交覆盖配置：允许时取最新有效版本并按其档位扣分。
func TestLateOverrideConfig(t *testing.T) {
	s := NewSystem()
	cfg := individualCfg()
	cfg.AllowLateOverride = true
	mustCreate(t, s, cfg)
	mustSubmit(t, s, 9, "a1", "st", "on-time")
	mustSubmit(t, s, 15, "a1", "st", "late") // late=5 -> 0.3
	sett, err := s.Settle(21, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got := sett[0]; got.VersionNo != 2 || got.Penalty != 0.3 || !got.Valid {
		t.Fatalf("override should pick latest valid v2, got %+v", got)
	}
}

// 首次提交之后加入小组须报可区分错误。
func TestJoinAfterFirstSubmit(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, groupCfg())
	mustJoin(t, s, 1, "g1", "team", "alice")
	mustSubmit(t, s, 2, "g1", "alice", "v1")
	mustCode(t, s.JoinGroup(3, "g1", "team", "bob"), ErrStateNotAllowed)
}

// 结算后全部状态冻结；重复结算报已结算。
func TestSettleFreezesEverything(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, groupCfg())
	mustJoin(t, s, 1, "g1", "team", "alice")
	mustSubmit(t, s, 5, "g1", "alice", "v1")
	if _, err := s.Settle(21, "g1"); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Settle(22, "g1"), ErrAlreadySettled)
	_, err := s.Submit(22, "g1", "alice", "x")
	mustCode(t, err, ErrAlreadySettled)
	_, err = s.GrantPersonalExtension(22, "g1", "alice", 1)
	mustCode(t, err, ErrAlreadySettled)
	_, err = s.GrantGroupExtension(22, "g1", "team", 1)
	mustCode(t, err, ErrAlreadySettled)
	mustCode(t, s.JoinGroup(22, "g1", "team", "bob"), ErrAlreadySettled)
	mustCode(t, s.LeaveGroup(22, "g1", "alice"), ErrAlreadySettled)
	mustCode(t, s.SelectVersion(22, "g1", "alice", 1), ErrAlreadySettled)
}

// 并发提交：结果等价于某个串行顺序，版本号连续无洞。
func TestConcurrentSubmitsLinearizable(t *testing.T) {
	s := NewSystem()
	mustCreate(t, s, AssignmentConfig{ID: "a1", Deadline: 1000, HardClose: 2000, Tiers: stdTiers()})
	const workers = 16
	const perWorker = 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := s.Submit(1, "a1", "st", "x"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	vers := s.Versions("a1", "st")
	if len(vers) != workers*perWorker {
		t.Fatalf("versions=%d", len(vers))
	}
	for i, v := range vers {
		if v.No != i+1 {
			t.Fatalf("version numbers not consecutive at %d: %d", i, v.No)
		}
	}
}
