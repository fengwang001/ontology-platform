package review

import (
	"testing"
	"time"
)

func sevenDays(base time.Time) time.Time { return base.Add(7 * 24 * time.Hour) }

func TestRecusalFourSources(t *testing.T) {
	s := setupLarge(t) // 评委 1..9 单位 U1..U9，申报人 300 单位 UX

	// 来源一：同单位。
	mustAddReviewer(t, s, 20, "UX", "A")
	if ok, src, _ := s.IsRecused(300, 20); !ok || len(src) != 1 || src[0] != "同单位" {
		t.Fatalf("同单位回避判定错误: %v", src)
	}

	// 来源二：登记关系。
	if err := s.RegisterRelation(clockAt(1), 300, 1); err != nil {
		t.Fatal(err)
	}
	if ok, src, _ := s.IsRecused(300, 1); !ok || !contains(src, "登记关系") {
		t.Fatalf("登记关系回避错误: %v", src)
	}

	// 来源三：已受理回避申请。
	if err := s.AcceptRecusalRequest(clockAt(2), 300, 2); err != nil {
		t.Fatal(err)
	}
	if ok, src, _ := s.IsRecused(300, 2); !ok || !contains(src, "回避申请") {
		t.Fatalf("回避申请判定错误: %v", src)
	}

	// 来源四：作废评审前科。
	id, _, err := s.CreateReview(clockAt(3), 300, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetReview(id)
	panel := v.Panel
	for i, j := range panel {
		mustVote(t, s, clockAt(4+i), id, j, 1, Approve) // 全赞成 -> 宣布
	}
	v, _ = s.GetReview(id)
	if v.Status != StatusPublicity {
		t.Fatalf("全赞成应进入公示: %+v", v)
	}
	end := v.PublicityEnd
	if err := s.AcceptObjection(end.Add(-2*time.Second), id); err != nil {
		t.Fatalf("结束前一刻应能受理异议: %v", err)
	}
	if err := s.AdjudicateObjection(end.Add(time.Hour), id, true); err != nil {
		t.Fatalf("裁定成立: %v", err)
	}
	for _, j := range panel {
		ok, src, _ := s.IsRecused(300, j)
		if !ok || !contains(src, "作废前科") {
			t.Fatalf("原评委 %d 应有作废前科回避, src=%v", j, src)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, x := range ss {
		if x == want {
			return true
		}
	}
	return false
}

func TestSubstitutionMidRoundAndVersionTrace(t *testing.T) {
	s := setupLarge(t)
	id, panel, _ := s.CreateReview(clockAt(1), 300, 5, nil) // [1..5]

	mustVote(t, s, clockAt(2), id, 1, 1, Approve)
	mustVote(t, s, clockAt(3), id, 2, 1, Oppose)
	if err := s.RegisterRelation(clockAt(4), 300, 1); err != nil {
		t.Fatal(err)
	}
	sub, err := s.HandleRecusal(clockAt(5), id, 1)
	if err != nil {
		t.Fatalf("回避替补: %v", err)
	}
	if sub != 6 {
		t.Fatalf("最小可行替补应为 6，实际 %d", sub)
	}
	v, _ := s.GetReview(id)
	if !intsEqual(v.Panel, []int{2, 3, 4, 5, 6}) || v.Version != 2 {
		t.Fatalf("新版本构成错误: %v v=%d", v.Panel, v.Version)
	}

	// 评委 1 的票作废但留痕保留；版本 1 仍可还原当时两张有效票。
	recs, _ := s.VoteRecords(id, 1)
	if len(recs) != 1 || !recs[0].Voided || recs[0].PanelVersion != 1 {
		t.Fatalf("评委1留痕应保留且标记作废: %+v", recs)
	}
	old, _ := s.EffectiveVotesAtVersion(id, 1, 1)
	if len(old) != 2 {
		t.Fatalf("版本1应还原 2 张有效票，实际 %d", len(old))
	}
	cur, _ := s.EffectiveVotesAtVersion(id, 2, 1)
	if len(cur) != 1 || cur[0].ReviewerID != 2 {
		t.Fatalf("版本2有效票应只剩评委2: %+v", cur)
	}

	// 票未补齐前不得结算。
	mustVote(t, s, clockAt(6), id, 3, 1, Approve)
	mustVote(t, s, clockAt(7), id, 4, 1, Approve)
	mustVote(t, s, clockAt(8), id, 5, 1, Approve)
	v, _ = s.GetReview(id)
	if len(v.History) != 0 || v.CurrentRound != 1 {
		t.Fatalf("替补票未补齐前不得结算: %+v", v.History)
	}
	// 此时仍在投票中且票未齐：无权限压过一切，重复投票可判别。
	if err := s.Vote(clockAt(9), id, 9, 1, Approve); codeOf(err) != ErrNoPermission {
		t.Fatalf("外人投票应报无权限，实际 %v", err)
	}
	if err := s.Vote(clockAt(10), id, 2, 1, Oppose); codeOf(err) != ErrDuplicateVote {
		t.Fatalf("重复投票应报重复投票，实际 %v", err)
	}
	// 一轮一票不可更改：换一种选择同样拒绝。
	if err := s.Vote(clockAt(11), id, 2, 1, Abstain); codeOf(err) != ErrDuplicateVote {
		t.Fatalf("改票应按重复投票拒绝，实际 %v", err)
	}
	mustVote(t, s, clockAt(9), id, 6, 1, Approve)
	v, _ = s.GetReview(id)
	if len(v.History) != 1 {
		t.Fatalf("补齐后应恰有一次结算: %+v", v.History)
	}
	_ = panel
}

func TestSubstitutionReopensSettledRound(t *testing.T) {
	s := setupLarge(t)
	id, panel, _ := s.CreateReview(clockAt(1), 300, 5, nil)
	choices := []Choice{Approve, Approve, Approve, Oppose, Abstain}
	for i, c := range choices {
		mustVote(t, s, clockAt(2+i), id, panel[i], 1, c)
	}
	v, _ := s.GetReview(id)
	if v.History[0].Outcome != OutcomeReconsider || v.CurrentRound != 2 {
		t.Fatalf("应进入复议: %+v", v.History)
	}
	mustVote(t, s, clockAt(10), id, panel[0], 2, Approve)
	mustVote(t, s, clockAt(11), id, panel[1], 2, Oppose)

	if err := s.AcceptRecusalRequest(clockAt(12), 300, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleRecusal(clockAt(13), id, 1); err != nil {
		t.Fatalf("替补失败: %v", err)
	}
	v, _ = s.GetReview(id)
	if v.CurrentRound != 1 || v.Version != 2 || !v.History[0].Superseded {
		t.Fatalf("应回到第一轮且旧结算标记推翻: %+v", v)
	}
	recs, _ := s.VoteRecords(id, 0)
	if len(recs) != 7 { // 5 张第一轮 + 2 张旧复议，全部留痕
		t.Fatalf("全部投票留痕应保留(7 张)，实际 %d", len(recs))
	}
	if err := s.Vote(clockAt(14), id, 2, 2, Approve); codeOf(err) != ErrIllegalState {
		t.Fatalf("第一轮补齐前复议应关闭，实际 %v", err)
	}
	mustVote(t, s, clockAt(15), id, 6, 1, Approve)
	v, _ = s.GetReview(id)
	// 2,3,6 赞（3），4 反，5 弃：仍落入复议带。
	if v.History[1].Outcome != OutcomeReconsider || v.CurrentRound != 2 {
		t.Fatalf("重结算应仍为复议: %+v", v.History)
	}
	// 新一幕复议：评委 2 旧幕投过，现在可重新投，不报重复。
	mustVote(t, s, clockAt(16), id, 2, 2, Approve)
}

func TestNoSubstituteAborts(t *testing.T) {
	s := setupSmall(t) // U1 申报人可行者仅 4(B),5(B),6(A)
	id, _, _ := s.CreateReview(clockAt(1), 100, 3, map[string]int{"B": 2})
	if err := s.RegisterRelation(clockAt(2), 100, 4); err != nil {
		t.Fatal(err)
	}
	_, err := s.HandleRecusal(clockAt(3), id, 4)
	if codeOf(err) != ErrInsufficientReviewers {
		t.Fatalf("无法替补应报评委不足，实际 %v", err)
	}
	v, _ := s.GetReview(id)
	if v.Status != StatusAborted {
		t.Fatalf("评审应中止: %+v", v)
	}
	// 中止后释放评委：对另一名与 4 无关系的 U1 申报人，4/5/6 均可再被抽到，
	// 证明中止释放了全部占用。
	mustAddApplicant(t, s, 109, "U1")
	if _, panel, err := s.CreateReview(clockAt(4), 109, 1, map[string]int{"B": 1}); err != nil {
		t.Fatalf("中止应释放占用: %v", err)
	} else if !intsEqual(panel, []int{4}) {
		t.Fatalf("释放后应能抽到 4: %v", panel)
	}
}

func TestPublicityBoundaryAndObjectionMutex(t *testing.T) {
	s := setupLarge(t)
	id, panel, _ := s.CreateReview(clockAt(1), 300, 3, nil)
	for i, j := range panel {
		mustVote(t, s, clockAt(2+i), id, j, 1, Approve)
	}
	v, _ := s.GetReview(id)
	end := v.PublicityEnd
	if !end.Equal(sevenDays(v.AnnouncedAt)) {
		t.Fatalf("公示期应为整七个自然日")
	}

	// 公示期内受理异议。
	t1 := end.Add(-3 * time.Second)
	if err := s.AcceptObjection(t1, id); err != nil {
		t.Fatalf("期内应可受理: %v", err)
	}
	// 受理仅一次（含"结束时刻本身也不能再受理"）。
	if err := s.AcceptObjection(t1.Add(time.Second), id); codeOf(err) != ErrIllegalState {
		t.Fatalf("重复受理应拒绝，实际 %v", err)
	}
	if err := s.AcceptObjection(end, id); codeOf(err) != ErrIllegalState {
		t.Fatalf("结束时刻不再受理，实际 %v", err)
	}
	// 已受理未裁定，不得终局。
	if _, err := s.Finalize(end.Add(time.Hour), id); codeOf(err) != ErrIllegalState {
		t.Fatalf("异议未裁定不得终局，实际 %v", err)
	}
	// 公示结束前一刻先尝试终局：拒绝。
	if _, err := s.Finalize(end.Add(-time.Nanosecond), id); codeOf(err) != ErrIllegalState {
		t.Fatalf("结束前终局应拒绝，实际 %v", err)
	}
	// 裁定不成立（受理后、公示期内即可裁定）。
	if err := s.AdjudicateObjection(t1.Add(2*time.Second), id, false); err != nil {
		t.Fatalf("裁定不成立: %v", err)
	}
	// 裁定仅一次。
	if err := s.AdjudicateObjection(t1.Add(2*time.Second+time.Hour), id, true); codeOf(err) != ErrIllegalState {
		t.Fatalf("重复裁定应拒绝，实际 %v", err)
	}
	// 结束时刻恰好生效。
	st, err := s.Finalize(end, id)
	if err != nil || st != StatusFinalPass {
		t.Fatalf("结束时刻应终局通过，实际 st=%d err=%v", st, err)
	}
	// 终局仅一次。
	if _, err := s.Finalize(end.Add(time.Hour), id); codeOf(err) != ErrIllegalState {
		t.Fatalf("重复终局应拒绝，实际 %v", err)
	}
}

func TestObjectionNotAcceptedAtEndInstant(t *testing.T) {
	s2 := setupLarge(t)
	id2, panel2, _ := s2.CreateReview(clockAt(1), 300, 3, nil)
	for i, j := range panel2 {
		mustVote(t, s2, clockAt(2+i), id2, j, 1, Approve)
	}
	v2, _ := s2.GetReview(id2)
	// 公示结束时刻本身不再受理异议；但此时恰好可以终局生效。
	if err := s2.AcceptObjection(v2.PublicityEnd, id2); codeOf(err) != ErrIllegalState {
		t.Fatalf("结束时刻本身不受理异议，实际 %v", err)
	}
	if st, err := s2.Finalize(v2.PublicityEnd, id2); err != nil || st != StatusFinalPass {
		t.Fatalf("无异议时结束时刻终局通过: st=%d err=%v", st, err)
	}
}

func TestUpheldObjectionMakesReviewersAvoid(t *testing.T) {
	s := setupLarge(t)
	id, panel, _ := s.CreateReview(clockAt(1), 300, 3, nil)
	for i, j := range panel {
		mustVote(t, s, clockAt(2+i), id, j, 1, Approve)
	}
	v, _ := s.GetReview(id)
	end := v.PublicityEnd
	if err := s.AcceptObjection(end.Add(-time.Hour), id); err != nil {
		t.Fatal(err)
	}
	if err := s.AdjudicateObjection(end.Add(time.Hour), id, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetReview(id)
	if v.Status != StatusVoid {
		t.Fatalf("应作废: %+v", v)
	}
	// 原评委已自动回避且占用已释放：重新抽取 n=3 得到下一组 [4,5,6]。
	id2, panel2, err := s.CreateReview(end.Add(2*time.Hour), 300, 3, nil)
	if err != nil {
		t.Fatalf("作废后应可重新抽取: %v", err)
	}
	if !intsEqual(panel2, []int{4, 5, 6}) {
		t.Fatalf("原评委回避后应抽到 [4 5 6]，实际 %v (id=%d)", panel2, id2)
	}
}

func TestErrorPriority(t *testing.T) {
	s := setupSmall(t)

	// 参数非法 压过 时钟回退。
	_, _, err := s.CreateReview(clockAt(-100), 100, 4, nil)
	if codeOf(err) != ErrInvalidParam {
		t.Fatalf("应报参数非法，实际 %v", err)
	}
	// 参数非法 压过 不存在（申报人 999 不存在但人数为偶数）。
	_, _, err = s.CreateReview(clockAt(1), 999, 4, nil)
	if codeOf(err) != ErrInvalidParam {
		t.Fatalf("应报参数非法，实际 %v", err)
	}
	// 时钟回退 压过 不存在。
	_, _, err = s.CreateReview(clockAt(-100), 999, 3, nil)
	if codeOf(err) != ErrClockRollback {
		t.Fatalf("应报时钟回退，实际 %v", err)
	}
	// 不存在 压过 状态。
	if err := s.Vote(clockAt(1), 999, 1, 1, Approve); codeOf(err) != ErrNotFound {
		t.Fatalf("应报不存在，实际 %v", err)
	}
	// 非法 choice 属参数非法，压过不存在的评委。
	id, panel, _ := s.CreateReview(clockAt(1), 100, 3, map[string]int{"B": 2})
	if err := s.Vote(clockAt(2), id, 999, 1, Choice(7)); codeOf(err) != ErrInvalidParam {
		t.Fatalf("非法票型应报参数非法，实际 %v", err)
	}
	// 状态不允许 压过 无权限：评审作废后，非评委投票先撞状态。
	if err := s.RegisterRelation(clockAt(3), 100, panel[0]); err != nil {
		t.Fatal(err)
	}
	// 将评审推进到公示再作废（全赞成后受理并成立异议）。
	// 当前第一轮尚未投票；先让评审中止后验证状态优先级。
	if _, err := s.HandleRecusal(clockAt(4), id, panel[0]); err == nil {
		t.Fatal("B 组缺额应无替补")
	}
	if err := s.Vote(clockAt(5), id, 999, 1, Approve); codeOf(err) != ErrIllegalState {
		t.Fatalf("中止评审投票应先报状态不允许，实际 %v", err)
	}
	// 无权限 压过 重复投票：非评委无"已投"可言。
	s2 := setupSmall(t)
	id2, panel2, _ := s2.CreateReview(clockAt(1), 100, 3, map[string]int{"B": 2})
	if err := s2.Vote(clockAt(2), id2, 999, 1, Approve); codeOf(err) != ErrNoPermission {
		t.Fatalf("非评委投票应报无权限，实际 %v", err)
	}
	if err := s2.Vote(clockAt(2), id2, panel2[0], 1, Approve); err != nil {
		t.Fatal(err)
	}
	if err := s2.Vote(clockAt(3), id2, panel2[0], 1, Approve); codeOf(err) != ErrDuplicateVote {
		t.Fatalf("应报重复投票，实际 %v", err)
	}
}
