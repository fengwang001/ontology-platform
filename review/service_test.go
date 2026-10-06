package review

import (
	"strconv"
	"testing"
)

func itoa(i int) string { return strconv.Itoa(i) }

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDrawUniquenessAndRecusal(t *testing.T) {
	s := setupSmall(t)
	// 申报人 U1：同单位评委 1,2,3,7 回避；可行者 4(B),5(B),6(A)。
	// 抽取条件不可满足：A 组至少 3 人，但可行 A 组只有 6。
	if _, _, err := s.CreateReview(clockAt(2), 100, 3, map[string]int{"A": 3}); err == nil {
		t.Fatal("评委不足时必须报错")
	} else if codeOf(err) != ErrInsufficientReviewers {
		t.Fatalf("应报评委不足，实际 %v", err)
	}

	// 失败不占用评委：随后的正常抽取仍成功，且结果唯一为 [4 5 6]。
	id, panel, err := s.CreateReview(clockAt(3), 100, 3, map[string]int{"B": 2})
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if !intsEqual(panel, []int{4, 5, 6}) {
		t.Fatalf("唯一抽取结果应为 [4 5 6]，实际 %v", panel)
	}

	// 4,5,6 已被占用：另一名 U1 申报人无评委可用。
	mustAddApplicant(t, s, 101, "U1")
	if _, _, err := s.CreateReview(clockAt(4), 101, 3, map[string]int{"B": 1}); codeOf(err) != ErrInsufficientReviewers {
		t.Fatalf("占用后应评委不足，实际 %v", err)
	}
	_ = id
}

func TestDrawLexicographicallyMinimum(t *testing.T) {
	s := NewService(testEpoch)
	// 10 名同单位申报人无关的评委，组分布用于检验"字典序最小且满足下限"。
	groups := map[int]string{
		1: "A", 2: "B", 3: "A", 4: "B", 5: "A",
		6: "B", 7: "A", 8: "B", 9: "A", 10: "B",
	}
	for id, g := range groups {
		mustAddReviewer(t, s, id, "U"+itoa(id), g)
	}
	mustAddApplicant(t, s, 200, "UX")

	// n=5，B 组至少 3 人。贪心结果：取1(A)，2(B)，3(A)，4(B)，
	// 5(A) 若取则 B 只剩 6..10 中 5 个名额需 1 个 B，可以；
	// 但还要 5 人总数，需 0 名额——逐位贪心的最小序列为
	// [1,2,3,4,6]（5 与 6 之间取 5 会导致后续 B 不足？逐一校验于下）。
	brute := lexicographicallyMinPanel(t, s, 200, 5, map[string]int{"B": 3})
	_, panel, err := s.CreateReview(clockAt(1), 200, 5, map[string]int{"B": 3})
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if !intsEqual(panel, brute) {
		t.Fatalf("贪心结果 %v 与暴力最小 %v 不一致", panel, brute)
	}
	t.Logf("字典序最小评委组 = %v", panel)
}

func TestAbstentionCountedInTotal(t *testing.T) {
	s := setupSmall(t)
	id, _, _ := s.CreateReview(clockAt(1), 100, 3, map[string]int{"B": 2})
	// 1 弃权计入总人数：2 赞成恰为 ceil(2*3/3)=2，通过。
	mustVote(t, s, clockAt(2), id, 4, 1, Approve)
	mustVote(t, s, clockAt(3), id, 5, 1, Abstain)
	mustVote(t, s, clockAt(4), id, 6, 1, Approve)
	v, err := s.GetReview(id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != StatusPublicity || v.History[0].Outcome != OutcomePass {
		t.Fatalf("弃权计入总数但 2/3 赞成仍应通过: %+v", v)
	}

	// 对照：同样 2 赞成但 1 反对（无弃权）仍是 3 人总数，2 赞成同样通过。
	// 而 1 赞成 1 反对 1 弃权：1 <= floor(3/2) 不通过。
	mustAddApplicant(t, s, 103, "U1")
	// 4,5,6 被第一个评审占用：新评审无可用评委（同单位者本就回避）。
	if _, _, err := s.CreateReview(clockAt(5), 103, 3, nil); codeOf(err) != ErrInsufficientReviewers {
		t.Fatalf("占用期间应评委不足，实际 %v", err)
	}
}

func TestFailBandAndReconsiderFlow(t *testing.T) {
	s := setupLarge(t)
	id, panel, err := s.CreateReview(clockAt(1), 300, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	// n=5：3 赞 -> 复议；复议 3 赞严格过半通过。
	mustVote(t, s, clockAt(2), id, panel[0], 1, Approve)
	mustVote(t, s, clockAt(3), id, panel[1], 1, Approve)
	mustVote(t, s, clockAt(4), id, panel[2], 1, Approve)
	mustVote(t, s, clockAt(5), id, panel[3], 1, Oppose)
	mustVote(t, s, clockAt(6), id, panel[4], 1, Abstain)
	v, _ := s.GetReview(id)
	if v.CurrentRound != 2 || v.History[0].Outcome != OutcomeReconsider {
		t.Fatalf("应进入复议: %+v", v)
	}
	// 复议：2 赞不满足严格过半 -> 不通过。
	mustVote(t, s, clockAt(7), id, panel[0], 2, Approve)
	mustVote(t, s, clockAt(8), id, panel[1], 2, Approve)
	mustVote(t, s, clockAt(9), id, panel[2], 2, Oppose)
	mustVote(t, s, clockAt(10), id, panel[3], 2, Oppose)
	mustVote(t, s, clockAt(11), id, panel[4], 2, Abstain)
	v, _ = s.GetReview(id)
	if v.Status != StatusPublicity || v.History[1].Outcome != OutcomeFail {
		t.Fatalf("复议 2 赞应不通过: %+v", v)
	}
}
