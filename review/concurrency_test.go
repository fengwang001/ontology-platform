package review

import (
	"sort"
	"sync"
	"testing"
	"time"
)

// TestConcurrentEquivalentToSerial：6 个彼此独立的申报人的操作并发提交，
// 因唯一抽取规则与互斥串行化，结果等价于某个串行顺序；最终 6 个评审
// 全部终局通过，且每个面板都是该申报人的字典序唯一解。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	setup := func() *Service {
		s := NewService(testEpoch)
		for id := 1; id <= 30; id++ {
			mustAddReviewer(t, s, id, "U"+itoa(50+id%3), grpName(id))
		}
		for a := 0; a < 6; a++ {
			mustAddApplicant(t, s, 200+a, "UX")
		}
		return s
	}

	s := setup()
	var wg sync.WaitGroup
	// 同一时刻可被多个操作使用（服务只拒绝严格回退），因此并发交错
	// 无需依赖调度顺序即可保持时钟合法。
	createAt := testEpoch.Add(time.Hour)
	voteAt := createAt.Add(time.Hour)

	// 并发建评审。
	type rev struct {
		id    int
		panel []int
	}
	revs := make([]rev, 6)
	for a := 0; a < 6; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			id, panel, err := s.CreateReview(createAt, 200+a, 3, nil)
			if err != nil {
				t.Errorf("并发建评审失败: %v", err)
				return
			}
			revs[a] = rev{id, panel}
		}(a)
	}
	wg.Wait()

	// 6 个面板两两不相交（占用语义），并起来为最小的 18 个可行编号；
	// 面板的具体归属依赖串行交错，但每个面板在其被分配的时刻都是
	// "剩余可行评委中的字典序最小唯一解"，整体等价于某个串行顺序。
	seen := map[int]bool{}
	for a := 0; a < 6; a++ {
		for _, j := range revs[a].panel {
			if seen[j] {
				t.Fatalf("评委 %d 被多个评审同时占用", j)
			}
			seen[j] = true
		}
	}
	union := make([]int, 0, 18)
	for j := range seen {
		union = append(union, j)
	}
	sort.Ints(union)
	if len(union) != 18 {
		t.Fatalf("6 个三人面板应占用 18 名不同评委，实际 %v", union)
	}
	// 全体申报人同单位 UX，与评委单位 U50/U51/U52 均不同 => 可行者就是
	// 1..30；任意串行交错下先到先得，占用集合恒为编号 1..18。
	for i, j := range union {
		if j != i+1 {
			t.Fatalf("占用集合应与调度无关恒为 1..18，实际 %v", union)
		}
	}

	// 并发投票：每位评委恰好一票，缺一票都不结算。
	for a := 0; a < 6; a++ {
		id := revs[a].id
		for _, j := range revs[a].panel {
			wg.Add(1)
			go func(id, j int) {
				defer wg.Done()
				if err := s.Vote(voteAt, id, j, 1, Approve); err != nil {
					t.Errorf("并发投票: %v", err)
				}
			}(id, j)
		}
	}
	wg.Wait()

	// 并发终局（均在各自公示结束时刻或之后）。
	ids := make([]int, 6)
	for a := range revs {
		ids[a] = revs[a].id
	}
	for _, id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			v, err := s.GetReview(id)
			if err != nil {
				t.Error(err)
				return
			}
			if st, err := s.Finalize(v.PublicityEnd, id); err != nil || st != StatusFinalPass {
				t.Errorf("并发终局: st=%d err=%v", st, err)
			}
		}(id)
	}
	wg.Wait()

	for _, id := range ids {
		v, _ := s.GetReview(id)
		if v.Status != StatusFinalPass {
			t.Fatalf("评审 %d 应终局通过，实际 %s", id, statusName(v.Status))
		}
	}
}

// TestRejectedOperationChangesNothing：被拒操作既不改状态也不推进时钟。
func TestRejectedOperationChangesNothing(t *testing.T) {
	s := setupSmall(t)
	before := s.Now()

	if err := s.RegisterRelation(before.Add(-time.Hour), 100, 4); codeOf(err) != ErrClockRollback {
		t.Fatalf("应报时钟回退，实际 %v", err)
	}
	if !s.Now().Equal(before) {
		t.Fatal("被时钟回退拒绝后时钟不得推进")
	}

	t1 := before.Add(time.Hour)
	if err := s.RegisterRelation(t1, 100, 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateReview(t1.Add(-time.Minute), 100, 4, nil); codeOf(err) != ErrInvalidParam {
		t.Fatalf("应报参数非法，实际 %v", err)
	}
	if _, _, err := s.CreateReview(t1.Add(-time.Minute), 999, 3, nil); codeOf(err) != ErrClockRollback {
		t.Fatalf("更早时刻应报时钟回退，实际 %v", err)
	}
	if !s.Now().Equal(t1) {
		t.Fatalf("被拒操作不得推进时钟: now=%s t1=%s", s.Now(), t1)
	}

	// 失败抽取不占用评委：另一名与 4 无关系的申报人仍可抽到 [4 5 6]。
	mustAddApplicant(t, s, 107, "U1")
	if id, p, err := s.CreateReview(t1.Add(2*time.Hour), 107, 3, map[string]int{"A": 9}); err == nil {
		t.Fatalf("应评委不足，却成功 id=%d %v", id, p)
	}
	_, p2, err := s.CreateReview(t1.Add(3*time.Hour), 107, 3, map[string]int{"B": 2})
	if err != nil || !intsEqual(p2, []int{4, 5, 6}) {
		t.Fatalf("失败抽取不应占用评委: %v %v", p2, err)
	}
}
