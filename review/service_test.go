package review

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

type harness struct {
	svc *Service
	at  time.Time
}

func newHarness() *harness { return &harness{svc: NewService(t0), at: t0} }

func (h *harness) tick() time.Time { h.at = h.at.Add(time.Minute); return h.at }

func (h *harness) jump(d time.Duration) time.Time { h.at = h.at.Add(d); return h.at }

func (h *harness) addExperts(n int64, group func(i int64) string) {
	for i := int64(1); i <= n; i++ {
		if err := h.svc.AddExpert(h.tick(), i, fmt.Sprintf("U%d", i), group(i)); err != nil {
			panic(err)
		}
	}
}

func (h *harness) addApplicant(id int64, unit string) {
	if err := h.svc.AddApplicant(h.tick(), id, unit); err != nil {
		panic(err)
	}
}

func mustKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %v, got nil", want)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if e.Kind != want {
		t.Fatalf("want kind %v, got %v (%v)", want, e.Kind, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// voteAll 让评审现任评委按 choices 顺序投票（round 轮）。
func (h *harness) voteAll(t *testing.T, rid int64, round int, choices ...Choice) {
	t.Helper()
	view, ok := h.svc.ReviewView(rid)
	if !ok {
		t.Fatalf("review %d not found", rid)
	}
	if len(choices) != len(view.Members) {
		t.Fatalf("choices %d != members %d", len(choices), len(view.Members))
	}
	for i, m := range view.Members {
		mustOK(t, h.svc.Vote(h.tick(), rid, m, round, choices[i]))
	}
}

func viewStatus(t *testing.T, svc *Service, rid int64) ReviewView {
	t.Helper()
	v, ok := svc.ReviewView(rid)
	if !ok {
		t.Fatalf("review %d not found", rid)
	}
	return v
}

// 三分之二门槛：恰等通过、差一进入复议；过半门槛：恰等不通过。
func TestRound1ThresholdsExact(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	cases := []struct {
		name    string
		choices []Choice
		want    Outcome
	}{
		{"n5_approve4_exact_two_thirds_pass", []Choice{ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject}, OutcomePass},
		{"n5_approve3_one_short_revote", []Choice{ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject}, OutcomeRevote},
		{"n5_approve2_exact_half_fail", []Choice{ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject, ChoiceReject}, OutcomeFail},
	}
	for i, tc := range cases {
		appID := int64(100 + i)
		h.addApplicant(appID, "UA")
		rid, panel, err := h.svc.CreateReview(h.tick(), appID, 5, nil)
		mustOK(t, err)
		if !reflect.DeepEqual(panel, []int64{1, 2, 3, 4, 5}) {
			t.Fatalf("%s: panel=%v", tc.name, panel)
		}
		h.voteAll(t, rid, 1, tc.choices...)
		v := viewStatus(t, h.svc, rid)
		if tc.want == OutcomeRevote {
			if v.Status != StatusActive || v.Result != nil {
				t.Fatalf("%s: want active revote, got %s result=%v", tc.name, v.Status, v.Result)
			}
			continue
		}
		if v.Status != StatusAnnounced || v.Result == nil || *v.Result != tc.want {
			t.Fatalf("%s: want announced %v, got %s result=%v", tc.name, tc.want, v.Status, v.Result)
		}
	}
	// n=3: 赞成 2 恰等 ceil(2/3*3)=2 通过；赞成 1 恰等 floor(3/2)=1 不通过。
	for i, tc := range []struct {
		choices []Choice
		want    Outcome
	}{
		{[]Choice{ChoiceApprove, ChoiceApprove, ChoiceReject}, OutcomePass},
		{[]Choice{ChoiceApprove, ChoiceReject, ChoiceAbstain}, OutcomeFail},
	} {
		appID := int64(200 + i)
		h.addApplicant(appID, "UA")
		rid, _, err := h.svc.CreateReview(h.tick(), appID, 3, nil)
		mustOK(t, err)
		h.voteAll(t, rid, 1, tc.choices...)
		v := viewStatus(t, h.svc, rid)
		if v.Result == nil || *v.Result != tc.want {
			t.Fatalf("n=3 case %d: want %v, got %v", i, tc.want, v.Result)
		}
	}
}

// 弃权计入总人数但不计赞成：3 赞成 2 弃权进复议（而非通过），4 赞成 1 弃权通过。
func TestAbstainCountsTowardTotal(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 5, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceAbstain, ChoiceAbstain)
	v := viewStatus(t, h.svc, rid)
	if v.Status != StatusActive || v.Result != nil {
		t.Fatalf("3 approve + 2 abstain: want revote, got %s %v", v.Status, v.Result)
	}
	h.addApplicant(101, "UA")
	rid2, _, err := h.svc.CreateReview(h.tick(), 101, 5, nil)
	mustOK(t, err)
	h.voteAll(t, rid2, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceAbstain)
	v2 := viewStatus(t, h.svc, rid2)
	if v2.Result == nil || *v2.Result != OutcomePass {
		t.Fatalf("4 approve + 1 abstain: want pass, got %v", v2.Result)
	}
}

// 复议须严格过半：n=5 时赞成 2 不通过，赞成 3 通过。
func TestRevoteStrictMajority(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	round1 := []Choice{ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject}
	// 复议赞成 2：2 不严格大于 5/2，不通过。
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 5, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, round1...)
	h.voteAll(t, rid, 2, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject, ChoiceReject)
	v := viewStatus(t, h.svc, rid)
	if v.Result == nil || *v.Result != OutcomeFail {
		t.Fatalf("revote 2/5: want fail, got %v", v.Result)
	}
	// 复议赞成 3：3 严格大于 5/2，通过。
	h.addApplicant(101, "UA")
	rid2, _, err := h.svc.CreateReview(h.tick(), 101, 5, nil)
	mustOK(t, err)
	h.voteAll(t, rid2, 1, round1...)
	h.voteAll(t, rid2, 2, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject)
	v2 := viewStatus(t, h.svc, rid2)
	if v2.Result == nil || *v2.Result != OutcomePass {
		t.Fatalf("revote 3/5: want pass, got %v", v2.Result)
	}
}

// 回避来源逐类验证：同单位、登记关系、已受理回避申请、以往已作废评审。
func TestRecusalSources(t *testing.T) {
	t.Run("same_unit", func(t *testing.T) {
		h := newHarness()
		h.addExperts(4, func(int64) string { return "G" })
		mustOK(t, h.svc.AddExpert(h.tick(), 9, "UA", "G"))
		h.addApplicant(100, "UA")
		_, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
		mustOK(t, err)
		for _, m := range panel {
			if m == 9 {
				t.Fatalf("same-unit expert 9 must be recused, panel=%v", panel)
			}
		}
	})
	t.Run("registered_relation", func(t *testing.T) {
		h := newHarness()
		h.addExperts(4, func(int64) string { return "G" })
		h.addApplicant(100, "UA")
		mustOK(t, h.svc.AddRelation(h.tick(), 100, 2))
		_, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
		mustOK(t, err)
		if !reflect.DeepEqual(panel, []int64{1, 3, 4}) {
			t.Fatalf("relation recusal: panel=%v", panel)
		}
	})
	t.Run("accepted_recusal_application", func(t *testing.T) {
		h := newHarness()
		h.addExperts(4, func(int64) string { return "G" })
		h.addApplicant(100, "UA")
		mustOK(t, h.svc.ApplyRecusal(h.tick(), 100, 3))
		// 未受理不影响抽取。
		_, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
		mustOK(t, err)
		if !reflect.DeepEqual(panel, []int64{1, 2, 3}) {
			t.Fatalf("pending application must not recuse, panel=%v", panel)
		}
		h.addApplicant(101, "UB")
		mustOK(t, h.svc.ApplyRecusal(h.tick(), 101, 3))
		mustOK(t, h.svc.AcceptRecusal(h.tick(), 101, 3))
		_, panel2, err := h.svc.CreateReview(h.tick(), 101, 3, nil)
		mustOK(t, err)
		if !reflect.DeepEqual(panel2, []int64{1, 2, 4}) {
			t.Fatalf("accepted application must recuse, panel=%v", panel2)
		}
	})
	t.Run("voided_review_history", func(t *testing.T) {
		h := newHarness()
		h.addExperts(6, func(int64) string { return "G" })
		h.addApplicant(100, "UA")
		rid, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
		mustOK(t, err)
		h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove)
		mustOK(t, h.svc.FileObjection(h.tick(), rid, "x"))
		mustOK(t, h.svc.RuleObjection(h.tick(), rid, true))
		if v := viewStatus(t, h.svc, rid); v.Status != StatusVoid {
			t.Fatalf("want void, got %s", v.Status)
		}
		_, panel2, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
		mustOK(t, err)
		for _, m := range panel {
			for _, m2 := range panel2 {
				if m == m2 {
					t.Fatalf("voided-review panelist %d must be recused, panel=%v", m, panel2)
				}
			}
		}
		if !reflect.DeepEqual(panel2, []int64{4, 5, 6}) {
			t.Fatalf("panel=%v", panel2)
		}
	})
}

// 抽取唯一性：结果为满足条件集合中编号升序字典序最小者。
func TestDrawUniquenessLexicographicMin(t *testing.T) {
	h := newHarness()
	// 1(G1) 2(G1) 3(G2) 4(G2) 5(G1)，要求 G2 至少 2 人，n=3。
	h.addExperts(5, func(i int64) string {
		if i == 3 || i == 4 {
			return "G2"
		}
		return "G1"
	})
	h.addApplicant(100, "UA")
	_, panel, err := h.svc.CreateReview(h.tick(), 100, 3, map[string]int{"G2": 2})
	mustOK(t, err)
	if !reflect.DeepEqual(panel, []int64{1, 3, 4}) {
		t.Fatalf("want lexicographic-min [1 3 4], got %v", panel)
	}
}

// 与测试内暴力枚举对照，验证若干随机配置下抽取结果的字典序最小性。
func TestDrawMatchesBruteForce(t *testing.T) {
	h := newHarness()
	groups := []string{"G1", "G1", "G2", "G2", "G1", "G2", "G1"}
	h.addExperts(7, func(i int64) string { return groups[i-1] })
	h.addApplicant(100, "UA")
	mustOK(t, h.svc.AddRelation(h.tick(), 100, 2))
	configs := []struct {
		n    int
		mins map[string]int
	}{
		{3, map[string]int{"G2": 2}},
		{5, map[string]int{"G1": 2, "G2": 2}},
		{3, nil},
		{1, map[string]int{"G2": 1}},
	}
	for ci, cfg := range configs {
		appID := int64(200 + ci)
		h.addApplicant(appID, "UB")
		if ci == 1 {
			mustOK(t, h.svc.ApplyRecusal(h.tick(), appID, 5))
			mustOK(t, h.svc.AcceptRecusal(h.tick(), appID, 5))
		}
		_, panel, err := h.svc.CreateReview(h.tick(), appID, cfg.n, cfg.mins)
		want, ok := bruteForceDraw(h.svc, appID, cfg.n, cfg.mins)
		if !ok {
			if err == nil {
				t.Fatalf("config %d: brute force infeasible but draw gave %v", ci, panel)
			}
			mustKind(t, err, ErrInsufficientExperts)
			continue
		}
		mustOK(t, err)
		if !reflect.DeepEqual(panel, want) {
			t.Fatalf("config %d: draw=%v brute-force=%v", ci, panel, want)
		}
	}
}

// bruteForceDraw 测试内独立实现的暴力枚举抽取（指数级，仅用于对照）。
func bruteForceDraw(svc *Service, appID int64, n int, mins map[string]int) ([]int64, bool) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	app := svc.applicants[appID]
	var cand []int64
	for _, id := range svc.expertOrder {
		e := svc.experts[id]
		if e.Unit == app.Unit || app.Relations[id] || app.RecusalAccepted[id] || app.VoidedExperts[id] {
			continue
		}
		cand = append(cand, id)
	}
	var best []int64
	var rec func(start int, cur []int64)
	rec = func(start int, cur []int64) {
		if len(cur) == n {
			counts := map[string]int{}
			for _, id := range cur {
				counts[svc.experts[id].Group]++
			}
			if !satisfies(counts, mins) {
				return
			}
			if best == nil || lessInts(cur, best) {
				best = append([]int64(nil), cur...)
			}
			return
		}
		if len(cur)+len(cand)-start < n {
			return
		}
		for i := start; i < len(cand); i++ {
			rec(i+1, append(cur, cand[i]))
		}
	}
	rec(0, nil)
	return best, best != nil
}

func lessInts(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// 评委不足：报错且不占用任何评委、不改变状态与时钟。
func TestInsufficientExperts(t *testing.T) {
	h := newHarness()
	h.addExperts(4, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	nowBefore := h.svc.Now()
	_, _, err := h.svc.CreateReview(h.tick(), 100, 5, nil)
	mustKind(t, err, ErrInsufficientExperts)
	if !h.svc.Now().Equal(nowBefore) {
		t.Fatalf("rejected op must not advance clock")
	}
	_, _, err = h.svc.CreateReview(h.tick(), 100, 3, map[string]int{"G2": 1})
	mustKind(t, err, ErrInsufficientExperts)
	if h.svc.Snapshot() == "" {
		t.Fatalf("snapshot must be non-empty")
	}
	rid, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	if rid != 1 || !reflect.DeepEqual(panel, []int64{1, 2, 3}) {
		t.Fatalf("failed draws must not consume experts or ids: rid=%d panel=%v", rid, panel)
	}
}

// 评审中途回避替补：票作废、版本生成、补齐前不得结算、
// 已宣布结果被取代并留痕、替补补投后重新结算。
func TestMidReviewRecusalSubstitution(t *testing.T) {
	h := newHarness()
	h.addExperts(6, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, panel, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceReject)
	announcedAt := h.at
	v := viewStatus(t, h.svc, rid)
	if v.Status != StatusAnnounced || v.Result == nil || *v.Result != OutcomePass {
		t.Fatalf("want announced pass, got %s %v", v.Status, v.Result)
	}
	// 新登记关系使评委 1 须回避：退出、票作废、4 号补入。
	mustOK(t, h.svc.AddRelation(h.tick(), 100, panel[0]))
	v = viewStatus(t, h.svc, rid)
	if v.Status != StatusActive || v.Result != nil {
		t.Fatalf("after recusal: want active without result, got %s %v", v.Status, v.Result)
	}
	if !reflect.DeepEqual(v.Members, []int64{2, 3, 4}) {
		t.Fatalf("members=%v", v.Members)
	}
	if len(v.Versions) != 2 {
		t.Fatalf("versions=%d", len(v.Versions))
	}
	// 票记录：1 号的票作废于版本 1；版本 0 有效票 3 张，版本 1 有效票 2 张。
	records, err := h.svc.VoteRecords(rid)
	mustOK(t, err)
	if len(records) != 3 || records[0].VoidedAt != 1 || records[0].Version != 0 {
		t.Fatalf("records=%+v", records)
	}
	eff0, err := h.svc.EffectiveVotesAtVersion(rid, 0)
	mustOK(t, err)
	if len(eff0) != 3 {
		t.Fatalf("version 0 effective votes=%d", len(eff0))
	}
	eff1, err := h.svc.EffectiveVotesAtVersion(rid, 1)
	mustOK(t, err)
	if len(eff1) != 2 {
		t.Fatalf("version 1 effective votes=%d", len(eff1))
	}
	// 已退出评委投票报无权限。
	mustKind(t, h.svc.Vote(h.tick(), rid, panel[0], 1, ChoiceApprove), ErrNoPermission)
	// 票补齐前不得宣布该轮结果：留痕中旧结论已被取代。
	if len(v.Trail) != 1 || !v.Trail[0].Superseded {
		t.Fatalf("trail=%+v", v.Trail)
	}
	// 替补补投后重新结算，新结论生效并留痕，公示期自新宣布时刻起算。
	mustOK(t, h.svc.Vote(h.tick(), rid, 4, 1, ChoiceApprove))
	reAnnouncedAt := h.at
	v = viewStatus(t, h.svc, rid)
	if v.Status != StatusAnnounced || v.Result == nil || *v.Result != OutcomePass {
		t.Fatalf("want re-announced pass, got %s %v", v.Status, v.Result)
	}
	if len(v.Trail) != 2 || !v.Trail[0].Superseded || v.Trail[1].Superseded {
		t.Fatalf("trail=%+v", v.Trail)
	}
	if !v.PublicityStart.Equal(reAnnouncedAt) || v.PublicityStart.Equal(announcedAt) {
		t.Fatalf("publicity must restart at re-announcement: %v vs %v", v.PublicityStart, announcedAt)
	}
	if !v.PublicityEnd.Equal(reAnnouncedAt.Add(PublicityDuration)) {
		t.Fatalf("publicity end=%v", v.PublicityEnd)
	}
}

// 复议轮次中的替补：替补者须补投已完成的轮次，两轮票补齐前不得结算。
func TestSubstitutionDuringRevote(t *testing.T) {
	h := newHarness()
	h.addExperts(6, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 5, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceReject)
	// 复议中评委 2 已投，随后评委 1 的回避申请被受理。
	mustOK(t, h.svc.Vote(h.tick(), rid, 2, 2, ChoiceApprove))
	mustOK(t, h.svc.ApplyRecusal(h.tick(), 100, 1))
	mustOK(t, h.svc.AcceptRecusal(h.tick(), 100, 1))
	v := viewStatus(t, h.svc, rid)
	if !reflect.DeepEqual(v.Members, []int64{2, 3, 4, 5, 6}) {
		t.Fatalf("members=%v", v.Members)
	}
	// 第一轮的“复议”结论已被取代，不得宣布；替补须先补投第一轮。
	if v.Result != nil || v.Status != StatusActive {
		t.Fatalf("want active, got %s %v", v.Status, v.Result)
	}
	for _, e := range v.Trail {
		if !e.Superseded {
			t.Fatalf("all trail entries must be superseded before votes complete: %+v", v.Trail)
		}
	}
	// 替补 6 补投第一轮（赞成），第一轮重新结算仍为复议。
	mustOK(t, h.svc.Vote(h.tick(), rid, 6, 1, ChoiceApprove))
	v = viewStatus(t, h.svc, rid)
	if v.Result != nil {
		t.Fatalf("round 2 incomplete, must not have final result: %v", v.Result)
	}
	// 其余评委投完复议，替补最后投复议：3 赞成 2 反对，严格过半通过。
	mustOK(t, h.svc.Vote(h.tick(), rid, 3, 2, ChoiceApprove))
	mustOK(t, h.svc.Vote(h.tick(), rid, 4, 2, ChoiceReject))
	mustOK(t, h.svc.Vote(h.tick(), rid, 5, 2, ChoiceReject))
	v = viewStatus(t, h.svc, rid)
	if v.Result != nil {
		t.Fatalf("substitute has not voted round 2, must not settle: %v", v.Result)
	}
	mustOK(t, h.svc.Vote(h.tick(), rid, 6, 2, ChoiceApprove))
	v = viewStatus(t, h.svc, rid)
	if v.Result == nil || *v.Result != OutcomePass {
		t.Fatalf("want pass, got %v", v.Result)
	}
	// 留痕：第一轮“复议”出现两次（旧条目被取代，新条目生效）。
	var revoteEntries int
	for _, e := range v.Trail {
		if e.Round == 1 && e.Outcome == OutcomeRevote {
			revoteEntries++
		}
	}
	if revoteEntries != 2 {
		t.Fatalf("trail=%+v", v.Trail)
	}
}

// 无法替补时评审中止。
func TestSubstitutionImpossibleAborts(t *testing.T) {
	h := newHarness()
	h.addExperts(4, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	mustOK(t, h.svc.AddRelation(h.tick(), 100, 4))
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceReject)
	mustOK(t, h.svc.AddRelation(h.tick(), 100, 1))
	v := viewStatus(t, h.svc, rid)
	if v.Status != StatusAborted {
		t.Fatalf("want aborted, got %s", v.Status)
	}
	if len(v.Versions) != 2 || !reflect.DeepEqual(v.Versions[1].Members, []int64{2, 3}) {
		t.Fatalf("versions=%+v", v.Versions)
	}
	// 中止后投票报状态不允许。
	mustKind(t, h.svc.Vote(h.tick(), rid, 2, 1, ChoiceApprove), ErrStateNotAllowed)
}

// 公示期末刻恰等：结束时刻本身不再受理异议；裁定不成立则结果在结束时刻生效。
func TestPublicityBoundaryExact(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceReject)
	v := viewStatus(t, h.svc, rid)
	end := v.PublicityEnd
	// 结束前一纳秒可受理。
	mustOK(t, h.svc.FileObjection(end.Add(-time.Nanosecond), rid, "last-instant"))
	mustOK(t, h.svc.RuleObjection(end.Add(-time.Nanosecond), rid, false))
	h.at = end.Add(-time.Nanosecond)
	// 另一个评审：结束时刻本身不再受理。
	h.addApplicant(101, "UB")
	rid2, _, err := h.svc.CreateReview(h.tick(), 101, 3, nil)
	mustOK(t, err)
	h.voteAll(t, rid2, 1, ChoiceApprove, ChoiceApprove, ChoiceReject)
	v2 := viewStatus(t, h.svc, rid2)
	end2 := *v2.PublicityEnd
	mustOK(t, h.svc.AddExpert(end2, 9, "U9", "G")) // 推进时钟至结束时刻
	mustKind(t, h.svc.FileObjection(end2, rid2, "too-late"), ErrStateNotAllowed)
	h.at = end2
	if got := viewStatus(t, h.svc, rid2).Status; got != StatusEffective {
		t.Fatalf("at publicity end without objection: want effective, got %s", got)
	}
	if got := viewStatus(t, h.svc, rid).Status; got != StatusEffective {
		t.Fatalf("objection ruled down + period ended: want effective, got %s", got)
	}
	// 生效后异议相关操作均不允许。
	mustKind(t, h.svc.FileObjection(h.tick(), rid, "late"), ErrStateNotAllowed)
	mustKind(t, h.svc.RuleObjection(h.tick(), rid, true), ErrStateNotAllowed)
}

// 异议受理、裁定与终局生效互斥且仅能发生一次。
func TestObjectionOnceOnly(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceReject)
	mustKind(t, h.svc.RuleObjection(h.tick(), rid, true), ErrStateNotAllowed) // 未受理不能裁定
	mustOK(t, h.svc.FileObjection(h.tick(), rid, "a"))
	mustKind(t, h.svc.FileObjection(h.tick(), rid, "b"), ErrStateNotAllowed) // 只能受理一次
	mustOK(t, h.svc.RuleObjection(h.tick(), rid, true))
	mustKind(t, h.svc.RuleObjection(h.tick(), rid, false), ErrStateNotAllowed) // 只能裁定一次
	if got := viewStatus(t, h.svc, rid).Status; got != StatusVoid {
		t.Fatalf("want void, got %s", got)
	}
}

// 拒绝优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 > 无权限 > 评委不足 > 重复投票。
func TestRejectionPriority(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	past := h.at.Add(-time.Hour)
	// 同时违反多条规则时只报最高优先级。
	mustKind(t, h.svc.Vote(past, 999, 777, 9, Choice(0)), ErrInvalidParam) // 轮次非法
	mustKind(t, h.svc.Vote(past, 999, 777, 1, Choice(0)), ErrInvalidParam) // 选项非法
	mustKind(t, h.svc.Vote(past, 999, 777, 1, ChoiceApprove), ErrClockRollback)
	mustKind(t, h.svc.Vote(h.at, 999, 777, 1, ChoiceApprove), ErrNotFound)
	mustKind(t, h.svc.Vote(h.at, rid, 777, 1, ChoiceApprove), ErrNoPermission)
	mustOK(t, h.svc.Vote(h.tick(), rid, 1, 1, ChoiceApprove))
	mustKind(t, h.svc.Vote(h.tick(), rid, 1, 1, ChoiceReject), ErrDuplicateVote)
	// 已宣布结果的评审：状态不允许优先于无权限。
	mustOK(t, h.svc.Vote(h.tick(), rid, 2, 1, ChoiceApprove))
	mustOK(t, h.svc.Vote(h.tick(), rid, 3, 1, ChoiceReject)) // 2/3 赞成，通过并宣布
	mustKind(t, h.svc.Vote(h.tick(), rid, 777, 1, ChoiceApprove), ErrStateNotAllowed)
	// CreateReview 的优先级链。
	mustKind(t, func() error { _, _, e := h.svc.CreateReview(h.tick(), 999, 4, nil); return e }(), ErrInvalidParam)
	mustKind(t, func() error { _, _, e := h.svc.CreateReview(h.tick(), 999, 3, nil); return e }(), ErrNotFound)
	mustKind(t, func() error { _, _, e := h.svc.CreateReview(h.tick(), 100, 7, nil); return e }(), ErrInsufficientExperts)
	// 时钟回退优先于不存在。
	mustKind(t, h.svc.AddRelation(past, 999, 999), ErrClockRollback)
	mustKind(t, h.svc.AddRelation(h.at, 999, 1), ErrNotFound)
}

// 被拒绝的操作不改变任何状态与时钟。
func TestRejectionIsAtomic(t *testing.T) {
	h := newHarness()
	h.addExperts(5, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	before := h.svc.Snapshot()
	mustKind(t, h.svc.Vote(h.at.Add(-time.Hour), rid, 1, 1, ChoiceApprove), ErrClockRollback)
	mustKind(t, h.svc.Vote(h.at, rid, 99, 1, ChoiceApprove), ErrNoPermission)
	mustKind(t, h.svc.FileObjection(h.at, rid, "too-early"), ErrStateNotAllowed)
	mustKind(t, h.svc.AddExpert(h.at, 1, "UX", "G"), ErrStateNotAllowed)
	if h.svc.Snapshot() != before {
		t.Fatalf("rejected ops must not change state or clock")
	}
}

// 并发等价：并发投票的结果与同一票集合的串行执行一致（-race 下运行）。
func TestConcurrentVotesEquivalentToSerial(t *testing.T) {
	build := func() (*Service, int64, []int64) {
		h := newHarness()
		h.addExperts(5, func(int64) string { return "G" })
		h.addApplicant(100, "UA")
		rid, panel, err := h.svc.CreateReview(h.tick(), 100, 5, nil)
		mustOK(t, err)
		return h.svc, rid, panel
	}
	choices := []Choice{ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceAbstain}
	voteAt := t0.Add(time.Hour)
	// 串行基准。
	serial, ridS, panelS := build()
	for i, m := range panelS {
		mustOK(t, serial.Vote(voteAt, ridS, m, 1, choices[i]))
	}
	// 并发执行（同一逻辑时刻，消除时间因素）。
	conc, ridC, panelC := build()
	var wg sync.WaitGroup
	errs := make([]error, len(panelC))
	for i, m := range panelC {
		wg.Add(1)
		go func(i int, m int64) {
			defer wg.Done()
			errs[i] = conc.Vote(voteAt, ridC, m, 1, choices[i])
		}(i, m)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent vote %d: %v", i, err)
		}
	}
	if canonicalVotes(t, serial, ridS) != canonicalVotes(t, conc, ridC) {
		t.Fatalf("concurrent result differs from serial execution")
	}
	sv, _ := serial.ReviewView(ridS)
	cv, _ := conc.ReviewView(ridC)
	if sv.Status != cv.Status || *sv.Result != *cv.Result {
		t.Fatalf("serial %s/%v vs concurrent %s/%v", sv.Status, sv.Result, cv.Status, cv.Result)
	}
}

// canonicalVotes 以（轮次, 评委）排序的票集合，消除并发到达顺序的影响。
func canonicalVotes(t *testing.T, svc *Service, rid int64) string {
	t.Helper()
	records, err := svc.VoteRecords(rid)
	mustOK(t, err)
	parts := []string{}
	for _, v := range records {
		parts = append(parts, fmt.Sprintf("r%de%dc%dv%dvoid%d", v.Round, v.ExpertID, v.Choice, v.Version, v.VoidedAt))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// 并发重复投票：恰好一票成功，其余报重复投票。
func TestConcurrentDuplicateVote(t *testing.T) {
	h := newHarness()
	h.addExperts(3, func(int64) string { return "G" })
	h.addApplicant(100, "UA")
	rid, _, err := h.svc.CreateReview(h.tick(), 100, 3, nil)
	mustOK(t, err)
	voteAt := h.tick()
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = h.svc.Vote(voteAt, rid, 1, 1, ChoiceApprove)
		}(i)
	}
	wg.Wait()
	var okCount, dupCount int
	for _, err := range results {
		if err == nil {
			okCount++
		} else if e, ok := err.(*Error); ok && e.Kind == ErrDuplicateVote {
			dupCount++
		}
	}
	if okCount != 1 || dupCount != 1 {
		t.Fatalf("ok=%d dup=%d results=%v", okCount, dupCount, results)
	}
}

// 重放等价：相同操作序列作用于全新服务得到完全相同的快照。
func TestReplayDeterminism(t *testing.T) {
	scenario := func() string {
		h := newHarness()
		h.addExperts(7, func(i int64) string {
			if i%2 == 0 {
				return "G2"
			}
			return "G1"
		})
		h.addApplicant(100, "UA")
		h.addApplicant(101, "UB")
		mustOK(t, h.svc.AddRelation(h.tick(), 101, 2))
		rid, _, err := h.svc.CreateReview(h.tick(), 100, 5, map[string]int{"G2": 1})
		mustOK(t, err)
		h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove, ChoiceReject, ChoiceAbstain)
		mustOK(t, h.svc.ApplyRecusal(h.tick(), 100, 1))
		mustOK(t, h.svc.AcceptRecusal(h.tick(), 100, 1))
		v := viewStatus(t, h.svc, rid)
		for _, m := range v.Members {
			view2 := viewStatus(t, h.svc, rid)
			_ = view2
			voted := map[int64]bool{}
			for _, rec := range mustRecords(t, h.svc, rid) {
				if rec.Round == 1 && rec.VoidedAt < 0 {
					voted[rec.ExpertID] = true
				}
			}
			if !voted[m] {
				mustOK(t, h.svc.Vote(h.tick(), rid, m, 1, ChoiceApprove))
			}
		}
		rid2, _, err := h.svc.CreateReview(h.tick(), 101, 3, nil)
		mustOK(t, err)
		h.voteAll(t, rid2, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove)
		mustOK(t, h.svc.FileObjection(h.tick(), rid2, "q"))
		mustOK(t, h.svc.RuleObjection(h.tick(), rid2, false))
		return h.svc.Snapshot()
	}
	first := scenario()
	for i := 0; i < 3; i++ {
		if got := scenario(); got != first {
			t.Fatalf("replay %d diverged", i)
		}
	}
}

func mustRecords(t *testing.T, svc *Service, rid int64) []VoteRecord {
	t.Helper()
	records, err := svc.VoteRecords(rid)
	mustOK(t, err)
	return records
}

// 复杂度可验证性：抽取开销（回避判定次数）不随历史评审总数增长。
func TestDrawCostIndependentOfHistory(t *testing.T) {
	h := newHarness()
	h.addExperts(12, func(i int64) string {
		if i%2 == 0 {
			return "G2"
		}
		return "G1"
	})
	h.addApplicant(900, "UA")
	measure := func(appID int64) int64 {
		before := h.svc.Stats().RecusalChecks
		_, _, err := h.svc.CreateReview(h.tick(), appID, 3, map[string]int{"G1": 1})
		mustOK(t, err)
		return h.svc.Stats().RecusalChecks - before
	}
	d1 := measure(900)
	if d1 != 12 {
		t.Fatalf("draw must check each expert exactly once, got %d", d1)
	}
	// 制造 30 个已作废评审的历史。
	for j := 0; j < 30; j++ {
		appID := int64(1000 + j)
		h.addApplicant(appID, "UB")
		rid, _, err := h.svc.CreateReview(h.tick(), appID, 3, nil)
		mustOK(t, err)
		h.voteAll(t, rid, 1, ChoiceApprove, ChoiceApprove, ChoiceApprove)
		mustOK(t, h.svc.FileObjection(h.tick(), rid, "x"))
		mustOK(t, h.svc.RuleObjection(h.tick(), rid, true))
	}
	h.addApplicant(901, "UC")
	d2 := measure(901)
	if d1 != d2 {
		t.Fatalf("draw cost grew with history: %d -> %d", d1, d2)
	}
}
