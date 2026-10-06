package exam

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustCat(t *testing.T, err error, cat Category) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", cat)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Category != cat {
		t.Fatalf("expected category %v, got %v (%s)", cat, e.Category, e.Reason)
	}
}

func addQ(t *testing.T, e *Engine, id string, score, diff int, kps, groups []string) {
	t.Helper()
	mustOK(t, e.CreateQuestion(id, score, diff, kps, groups))
}

func newPaper(t *testing.T, e *Engine, id string, target int) {
	t.Helper()
	mustOK(t, e.CreatePaper(id, Constraints{TargetScore: target}))
}

func addToPaper(t *testing.T, e *Engine, pid string, qids ...string) {
	t.Helper()
	for _, q := range qids {
		mustOK(t, e.AddToPaper(pid, q))
	}
}

func TestTotalScoreExactAndOffByOne(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 5, 1, nil, nil)
	addQ(t, e, "q2", 5, 1, nil, nil)

	newPaper(t, e, "exact", 10)
	addToPaper(t, e, "exact", "q1", "q2")
	mustOK(t, e.Publish("exact"))

	newPaper(t, e, "minus-one", 9)
	addToPaper(t, e, "minus-one", "q1", "q2")
	mustCat(t, e.Publish("minus-one"), CatTotalScore)

	newPaper(t, e, "plus-one", 11)
	addToPaper(t, e, "plus-one", "q1", "q2")
	mustCat(t, e.Publish("plus-one"), CatTotalScore)

	// 发布失败不改变草稿：修正目标分后可发布。
	mustOK(t, e.RemoveFromPaper("plus-one", "q2"))
	addQ(t, e, "q3", 6, 1, nil, nil)
	addToPaper(t, e, "plus-one", "q3")
	mustOK(t, e.Publish("plus-one"))
}

func TestDifficultyRangeBoundaries(t *testing.T) {
	e := NewEngine()
	for _, id := range []string{"a", "b", "c", "d"} {
		addQ(t, e, id, 1, 1, nil, nil)
	}
	cons := func(target int) Constraints {
		return Constraints{TargetScore: target, Difficulty: map[int]DifficultyRange{1: {Min: 2, Max: 3}}}
	}
	// 下界取等：2 题通过。
	mustOK(t, e.CreatePaper("lo", cons(2)))
	addToPaper(t, e, "lo", "a", "b")
	mustOK(t, e.Publish("lo"))
	// 上界取等：3 题通过。
	mustOK(t, e.CreatePaper("hi", cons(3)))
	addToPaper(t, e, "hi", "a", "b", "c")
	mustOK(t, e.Publish("hi"))
	// 越上界：4 题失败。
	mustOK(t, e.CreatePaper("over", cons(4)))
	addToPaper(t, e, "over", "a", "b", "c", "d")
	mustCat(t, e.Publish("over"), CatDifficulty)
	// 越下界：1 题失败。
	mustOK(t, e.CreatePaper("under", cons(1)))
	addToPaper(t, e, "under", "a")
	mustCat(t, e.Publish("under"), CatDifficulty)
}

func TestKnowledgeCoverageExactCount(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "k1", 1, 1, []string{"K"}, nil)
	addQ(t, e, "k2", 1, 1, []string{"K"}, nil)
	addQ(t, e, "n1", 1, 1, []string{"J"}, nil)
	cons := Constraints{TargetScore: 2, Coverage: map[string]int{"K": 2}}
	// 覆盖数恰等于配置：通过。
	mustOK(t, e.CreatePaper("exact", cons))
	addToPaper(t, e, "exact", "k1", "k2")
	mustOK(t, e.Publish("exact"))
	// 覆盖数差一：失败。
	mustOK(t, e.CreatePaper("short", cons))
	addToPaper(t, e, "short", "k1", "n1")
	mustCat(t, e.Publish("short"), CatCoverage)
}

func TestMutexTransitiveClosureThreeGroups(t *testing.T) {
	e := NewEngine()
	// A-B 同组 g1，B-C 同组 g2，C-D 同组 g3：传递闭包下任意两题互斥。
	addQ(t, e, "A", 1, 1, nil, []string{"g1"})
	addQ(t, e, "B", 1, 1, nil, []string{"g1", "g2"})
	addQ(t, e, "C", 1, 1, nil, []string{"g2", "g3"})
	addQ(t, e, "D", 1, 1, nil, []string{"g3"})
	addQ(t, e, "E", 1, 1, nil, []string{"g4"})

	for _, pair := range [][2]string{{"A", "B"}, {"A", "C"}, {"A", "D"}, {"B", "C"}, {"B", "D"}, {"C", "D"}} {
		pid := "p-" + pair[0] + pair[1]
		newPaper(t, e, pid, 2)
		addToPaper(t, e, pid, pair[0], pair[1])
		mustCat(t, e.Publish(pid), CatMutexConflict)
	}
	// 跨分量不互斥。
	newPaper(t, e, "ok", 2)
	addToPaper(t, e, "ok", "A", "E")
	mustOK(t, e.Publish("ok"))
}

func TestReviseAfterPublishKeepsFrozen(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q", 5, 1, []string{"K"}, nil)
	addQ(t, e, "r", 5, 1, []string{"J"}, nil)
	newPaper(t, e, "p", 10)
	addToPaper(t, e, "p", "q", "r")
	mustOK(t, e.Publish("p"))

	if _, err := e.ReviseQuestion("q", 9, 3, []string{"Z"}); err != nil {
		t.Fatal(err)
	}
	state, entries, _, ok := e.PaperInfo("p")
	if !ok || state != Published {
		t.Fatalf("paper state = %v, want published", state)
	}
	for _, en := range entries {
		if en.QuestionID == "q" {
			if en.Version != 1 || en.Score != 5 || en.Difficulty != 1 || len(en.KnowledgePoints) != 1 || en.KnowledgePoints[0] != "K" {
				t.Fatalf("published entry changed after revision: %+v", en)
			}
		}
	}
}

func TestSuspendedStaysInPublishedPaper(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 1, 1, nil, nil)
	addQ(t, e, "q2", 1, 1, nil, nil)
	newPaper(t, e, "p", 2)
	addToPaper(t, e, "p", "q1", "q2")
	mustOK(t, e.Publish("p"))

	mustOK(t, e.SuspendQuestion("q1"))
	if state, _, _, _ := e.PaperInfo("p"); state != Published {
		t.Fatalf("paper with suspended question must stay published, got %v", state)
	}
	// 停用题不得被新组卷选用。
	newPaper(t, e, "p2", 1)
	mustCat(t, e.AddToPaper("p2", "q1"), CatNotSelectable)
	// 恢复后可用。
	mustOK(t, e.ResumeQuestion("q1"))
	mustOK(t, e.AddToPaper("p2", "q1"))
}

func TestWithdrawInvalidatesAndReplaceRestores(t *testing.T) {
	e := NewEngine()
	for _, id := range []string{"q1", "q2", "q3", "q4", "q5", "q6"} {
		addQ(t, e, id, 1, 1, nil, nil)
	}
	newPaper(t, e, "p1", 2)
	addToPaper(t, e, "p1", "q1", "q2")
	newPaper(t, e, "p2", 2)
	addToPaper(t, e, "p2", "q1", "q3")
	newPaper(t, e, "p3", 2)
	addToPaper(t, e, "p3", "q2", "q3")
	for _, p := range []string{"p1", "p2", "p3"} {
		mustOK(t, e.Publish(p))
	}

	affected, err := e.WithdrawQuestion("q1")
	mustOK(t, err)
	if len(affected) != 2 || affected[0] != "p1" || affected[1] != "p2" {
		t.Fatalf("affected papers = %v, want [p1 p2]", affected)
	}
	for _, p := range []string{"p1", "p2"} {
		if state, _, _, _ := e.PaperInfo(p); state != Invalid {
			t.Fatalf("paper %s state = %v, want invalid", p, state)
		}
	}

	// 替换下架题后恢复已发布。
	mustOK(t, e.Replace("p1", "q1", "q4"))
	if state, _, _, _ := e.PaperInfo("p1"); state != Published {
		t.Fatalf("p1 should be republished after replacement")
	}

	// p3 含两道下架题：替换一道仍失效但替换生效，再替换另一道才恢复。
	if _, err := e.WithdrawQuestion("q2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.WithdrawQuestion("q3"); err != nil {
		t.Fatal(err)
	}
	if state, _, _, _ := e.PaperInfo("p3"); state != Invalid {
		t.Fatalf("p3 state = %v, want invalid", state)
	}
	mustOK(t, e.Replace("p3", "q2", "q5"))
	state, entries, _, _ := e.PaperInfo("p3")
	if state != Invalid {
		t.Fatalf("p3 still contains withdrawn q3, must stay invalid, got %v", state)
	}
	found := false
	for _, en := range entries {
		if en.QuestionID == "q5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("replacement must take effect even while paper stays invalid: %v", entries)
	}
	mustOK(t, e.Replace("p3", "q3", "q6"))
	if state, _, _, _ := e.PaperInfo("p3"); state != Published {
		t.Fatalf("p3 should recover after all withdrawn questions replaced")
	}
}

func TestGroupChangeAfterPublishNoBacktrack(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "A", 1, 1, nil, nil)
	addQ(t, e, "B", 1, 1, nil, nil)
	addQ(t, e, "C", 1, 1, nil, nil)
	newPaper(t, e, "p", 2)
	addToPaper(t, e, "p", "A", "B")
	mustOK(t, e.Publish("p"))

	// 发布后把 A、B 移入同组：已发布试卷状态不回溯。
	mustOK(t, e.SetQuestionGroups("A", []string{"g"}))
	mustOK(t, e.SetQuestionGroups("B", []string{"g"}))
	if state, _, _, _ := e.PaperInfo("p"); state != Published {
		t.Fatalf("group change must not affect published paper, got %v", state)
	}
	// 但影响此后的替换判定：C 与 A 同组时替换被拒绝。
	mustOK(t, e.SetQuestionGroups("C", []string{"g"}))
	mustCat(t, e.Replace("p", "B", "C"), CatMutexConflict)
	// C 退出互斥组后替换成功。
	mustOK(t, e.SetQuestionGroups("C", nil))
	mustOK(t, e.Replace("p", "B", "C"))
}

func TestStateNotAllowedOps(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 1, 1, nil, nil)
	addQ(t, e, "q2", 1, 1, nil, nil)
	newPaper(t, e, "p", 2)
	addToPaper(t, e, "p", "q1", "q2")

	// 对草稿做替换：状态不允许。
	mustCat(t, e.Replace("p", "q1", "q2"), CatStateNotAllowed)
	mustOK(t, e.Publish("p"))
	// 对已发布试卷做草稿操作：状态不允许。
	mustCat(t, e.AddToPaper("p", "q1"), CatStateNotAllowed)
	mustCat(t, e.RemoveFromPaper("p", "q1"), CatStateNotAllowed)
	mustCat(t, e.ValidateDraft("p"), CatStateNotAllowed)
	mustCat(t, e.Publish("p"), CatStateNotAllowed)
}

func TestLifecycleTransitions(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q", 1, 1, nil, nil)
	// 非法迁移均可区分地拒绝。
	mustCat(t, e.ResumeQuestion("q"), CatStateNotAllowed) // 可用 -> 可用
	mustOK(t, e.SuspendQuestion("q"))
	mustCat(t, e.SuspendQuestion("q"), CatStateNotAllowed) // 停用 -> 停用
	mustOK(t, e.ResumeQuestion("q"))
	if _, err := e.WithdrawQuestion("q"); err != nil {
		t.Fatal(err)
	}
	mustCat(t, e.SuspendQuestion("q"), CatStateNotAllowed) // 下架 -> 停用
	mustCat(t, e.ResumeQuestion("q"), CatStateNotAllowed)  // 下架 -> 可用
	if _, err := e.WithdrawQuestion("q"); !IsCategory(err, CatStateNotAllowed) {
		t.Fatalf("withdraw withdrawn: want state-not-allowed, got %v", err)
	}
}

func TestVersionNotConsumedOnRejectedRevise(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q", 1, 1, nil, nil)
	if _, err := e.ReviseQuestion("q", -1, 1, nil); !IsCategory(err, CatInvalidParam) {
		t.Fatalf("want invalid-param, got %v", err)
	}
	if _, err := e.ReviseQuestion("ghost", 1, 1, nil); !IsCategory(err, CatNotFound) {
		t.Fatalf("want not-found, got %v", err)
	}
	n, err := e.ReviseQuestion("q", 2, 2, []string{"K"})
	mustOK(t, err)
	if n != 2 {
		t.Fatalf("rejected revisions must not consume version numbers, got version %d, want 2", n)
	}
}

func TestInvalidPaperReplaceNonWithdrawnRejected(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "w", 1, 1, nil, nil)
	addQ(t, e, "keep", 1, 1, nil, nil)
	addQ(t, e, "cand", 1, 1, nil, nil)
	newPaper(t, e, "p", 2)
	addToPaper(t, e, "p", "w", "keep")
	mustOK(t, e.Publish("p"))
	if _, err := e.WithdrawQuestion("w"); err != nil {
		t.Fatal(err)
	}
	// 失效试卷以非下架题为替换对象：拒绝且可区分。
	err := e.Replace("p", "keep", "cand")
	mustCat(t, err, CatStateNotAllowed)
	if !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("reason must identify the withdrawn-target rule, got %v", err)
	}
	// 以下架题为对象则允许。
	mustOK(t, e.Replace("p", "w", "cand"))
	if state, _, _, _ := e.PaperInfo("p"); state != Published {
		t.Fatalf("paper should recover, got %v", state)
	}
}

func TestErrorPriorityPairs(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "s1", 1, 1, []string{"K"}, []string{"g"})
	addQ(t, e, "s2", 1, 1, nil, []string{"g"}) // 与 s1 互斥
	addQ(t, e, "s3", 1, 1, nil, nil)

	// 参数非法 > 不存在：id 不存在且分值非法，报参数非法。
	if _, err := e.ReviseQuestion("ghost", -1, 1, nil); !IsCategory(err, CatInvalidParam) {
		t.Fatalf("invalid-param must beat not-found, got %v", err)
	}
	// 不存在 > 状态不允许：对不存在的题做非法迁移，报不存在。
	mustCat(t, e.SuspendQuestion("ghost"), CatNotFound)
	mustCat(t, e.Publish("ghost"), CatNotFound)

	// 状态不允许 > 不可选用：已发布试卷 + 停用题，报状态不允许。
	newPaper(t, e, "pub", 1)
	addToPaper(t, e, "pub", "s3")
	mustOK(t, e.Publish("pub"))
	mustOK(t, e.SuspendQuestion("s3"))
	mustCat(t, e.AddToPaper("pub", "s3"), CatStateNotAllowed)
	mustOK(t, e.ResumeQuestion("s3"))

	// 不可选用 > 互斥：草稿同时含停用题与互斥对，报不可选用。
	newPaper(t, e, "d1", 3)
	addToPaper(t, e, "d1", "s1", "s2", "s3")
	mustOK(t, e.SuspendQuestion("s3"))
	mustCat(t, e.Publish("d1"), CatNotSelectable)
	mustOK(t, e.ResumeQuestion("s3"))

	// 互斥 > 总分：互斥对且总分不符，报互斥。
	mustCat(t, e.Publish("d1"), CatMutexConflict)

	// 总分 > 覆盖：解除互斥后总分不符且覆盖不足，报总分。
	mustOK(t, e.SetQuestionGroups("s2", nil))
	mustOK(t, e.CreatePaper("d2", Constraints{TargetScore: 99, Coverage: map[string]int{"K": 5}}))
	addToPaper(t, e, "d2", "s1", "s2")
	mustCat(t, e.Publish("d2"), CatTotalScore)

	// 覆盖 > 难度：总分修正后覆盖不足且难度越界，报覆盖。
	mustOK(t, e.CreatePaper("d3", Constraints{
		TargetScore: 2,
		Coverage:    map[string]int{"K": 2},
		Difficulty:  map[int]DifficultyRange{2: {Min: 1, Max: 1}},
	}))
	addToPaper(t, e, "d3", "s1", "s2")
	mustCat(t, e.Publish("d3"), CatCoverage)
}

func TestMutexCheckCostIndependentOfTotals(t *testing.T) {
	e := NewEngine()
	// 目标连通分量：x-y-z 链（2 个组、3 道题）。
	addQ(t, e, "x", 1, 1, nil, []string{"c1"})
	addQ(t, e, "y", 1, 1, nil, []string{"c1", "c2"})
	addQ(t, e, "z", 1, 1, nil, []string{"c2"})

	// 制造大量无关题目与互斥组。
	addBulk := func(prefix string, n int) {
		for i := 0; i < n; i++ {
			id := prefix + "-" + strconv.Itoa(i)
			if err := e.CreateQuestion(id, 1, 1, nil, []string{id + "-g"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	addBulk("b1", 2000)
	_, visited1 := e.CheckConflict("x", []string{"z"})
	addBulk("b2", 4000)
	hit, visited2 := e.CheckConflict("x", []string{"z"})
	if hit != "z" {
		t.Fatalf("expected conflict with z, got %q", hit)
	}
	if visited1 != visited2 {
		t.Fatalf("visited count grew with totals: %d -> %d", visited1, visited2)
	}
	if visited1 != 3 {
		t.Fatalf("visited = %d, want exactly the component size 3", visited1)
	}
	// 无冲突时同样只遍历本分量。
	if _, visited := e.CheckConflict("x", []string{"b1-0"}); visited != 3 {
		t.Fatalf("non-conflict check visited = %d, want 3", visited)
	}
}

// TestConcurrentOps 并发调用全部操作（配合 -race），
// 结束后验证串行化不变量：已发布试卷不含下架题，
// 失效试卷恰好因含下架题而失效，且冻结条目仍满足分值/覆盖/难度约束。
func TestConcurrentOps(t *testing.T) {
	e := NewEngine()
	const nQ, nP = 12, 4
	qids := make([]string, nQ)
	for i := range qids {
		qids[i] = fmt.Sprintf("q%d", i)
		addQ(t, e, qids[i], 1+i%3, 1+i%3, []string{fmt.Sprintf("K%d", i%2)}, nil)
	}
	pids := make([]string, nP)
	for i := range pids {
		pids[i] = fmt.Sprintf("p%d", i)
		mustOK(t, e.CreatePaper(pids[i], Constraints{TargetScore: 3}))
	}
	cons := map[string]Constraints{}
	for _, p := range pids {
		cons[p] = Constraints{TargetScore: 3}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				q := qids[r.Intn(nQ)]
				p := pids[r.Intn(nP)]
				switch r.Intn(9) {
				case 0:
					_, _ = e.ReviseQuestion(q, 1+r.Intn(3), 1+r.Intn(3), []string{fmt.Sprintf("K%d", r.Intn(2))})
				case 1:
					_ = e.SuspendQuestion(q)
				case 2:
					_ = e.ResumeQuestion(q)
				case 3:
					_, _ = e.WithdrawQuestion(q)
				case 4:
					_ = e.SetQuestionGroups(q, []string{fmt.Sprintf("g%d", r.Intn(3))})
				case 5:
					_ = e.AddToPaper(p, q)
				case 6:
					_ = e.RemoveFromPaper(p, q)
				case 7:
					_ = e.Publish(p)
				case 8:
					_ = e.Replace(p, qids[r.Intn(nQ)], qids[r.Intn(nQ)])
				}
			}
		}(int64(g*1000 + 7))
	}
	wg.Wait()

	for _, pid := range pids {
		state, entries, _, ok := e.PaperInfo(pid)
		if !ok {
			t.Fatalf("paper %s missing", pid)
		}
		withdrawn := 0
		for _, en := range entries {
			life, _, _, _ := e.QuestionInfo(en.QuestionID)
			if life == Withdrawn {
				withdrawn++
			}
		}
		switch state {
		case Published:
			if withdrawn > 0 {
				t.Fatalf("published paper %s contains %d withdrawn questions", pid, withdrawn)
			}
			if err := validateEntries("invariant", entries, cons[pid], e.mi); err != nil {
				// 互斥组可能已变更，只断言分值/覆盖/难度口径。
				if !IsCategory(err, CatMutexConflict) {
					t.Fatalf("published paper %s violates frozen constraints: %v", pid, err)
				}
			}
		case Invalid:
			if withdrawn == 0 {
				t.Fatalf("invalid paper %s contains no withdrawn question", pid)
			}
		}
	}
}
