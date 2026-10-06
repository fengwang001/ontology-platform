package cedu

import (
	"sync"
	"testing"
)

// TestConcurrentEquivalence 验证并发调用无数据竞争（请配合 -race），
// 且同一 now 下的批量并发登记与串行登记得到相同的总量核算结论。
func TestConcurrentEquivalence(t *testing.T) {
	cfg := testCfg()

	build := func(parallel bool) int {
		svc, _ := NewService(cfg)
		if err := svc.RegisterHolder(RegisterInput{HolderID: "c", IssueDate: 0, Now: 0}); err != nil {
			t.Fatal(err)
		}
		do := func(g int) {
			for i := 0; i < 20; i++ {
				_, _ = svc.RegisterCredit(CreditInput{
					HolderID: "c", Category: Category(g % 3),
					Credits: 1, EarnedOn: 5, Now: 5,
					Org: orgName(g, i),
				})
			}
		}
		if parallel {
			var wg sync.WaitGroup
			for g := 0; g < 8; g++ {
				wg.Add(1)
				go func(g int) { defer wg.Done(); do(g) }(g)
			}
			wg.Wait()
		} else {
			for g := 0; g < 8; g++ {
				do(g)
			}
		}
		st, err := svc.Status("c", 5)
		if err != nil {
			t.Fatal(err)
		}
		return st.CurrentCycle.TotalIn
	}

	// 并发执行多次，结果必须恒等于串行结果（等价于某个串行顺序）。
	serial := build(false)
	for i := 0; i < 20; i++ {
		if got := build(true); got != serial {
			t.Fatalf("parallel total %d != serial %d", got, serial)
		}
	}
}

func orgName(g, i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "org-" + string(letters[g]) + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// BenchmarkStatusFixedCost 证明周期核算开销不随持证人历史记录总量增长：
// 预置 N 条历史记录（分散在已终结周期），Status 只读取当前周期的
// 聚合桶与相邻缓存，耗时应基本恒定。
func BenchmarkStatusFixedCost(b *testing.B) {
	cfg := Config{
		CycleLength: 10, TotalRequired: 1000000, RequiredMin: 1,
		RequiredCap: 1000000, ElectiveCap: 1000000, GraceDays: 0,
		CorrectDays: 10000000, CarryoverCap: 0,
	}
	measure := func(records int) func(b *testing.B) {
		return func(b *testing.B) {
			svc, _ := NewService(cfg)
			if err := svc.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < records; i++ {
				day := i % 100
				now := day
				_, _ = svc.RegisterCredit(CreditInput{
					HolderID: "h", Category: Elective, Credits: 1,
					EarnedOn: day, Org: "org" + itoa(i), Now: now,
				})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := svc.Status("h", 50); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	b.Run("100records", measure(100))
	b.Run("10000records", measure(10000))
}

func TestHistoryAtMatchesPastReality(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 30, EarnedOn: 2, Org: "o1", Now: 2})
	stAt2, _ := s.Status("h", 2)
	reg(t, s, CreditInput{HolderID: "h", Category: Online, Credits: 5, EarnedOn: 4, Org: "o2", Now: 4})
	stAt4, _ := s.Status("h", 4)
	reg(t, s, CreditInput{HolderID: "h", Category: Online, Credits: 7, EarnedOn: 6, Org: "o3", Now: 6})

	got2, err := s.HistoryAt("h", 2)
	if err != nil {
		t.Fatal(err)
	}
	got4, err := s.HistoryAt("h", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got2.CurrentCycle != stAt2.CurrentCycle {
		t.Fatalf("history@2:\ngot =%+v\nwant=%+v", got2.CurrentCycle, stAt2.CurrentCycle)
	}
	if got4.CurrentCycle != stAt4.CurrentCycle {
		t.Fatalf("history@4:\ngot =%+v\nwant=%+v", got4.CurrentCycle, stAt4.CurrentCycle)
	}
	// 历史时刻 2 不应看到 4 日和 6 日的新增学分。
	if got2.CurrentCycle.TotalIn != 20 {
		t.Fatalf("history@2 total want 20 (elective capped), got %d", got2.CurrentCycle.TotalIn)
	}
	if got4.CurrentCycle.TotalIn != 25 {
		t.Fatalf("history@4 total want 25, got %d", got4.CurrentCycle.TotalIn)
	}
}

func TestClockRollbackRejectionLeavesState(t *testing.T) {
	s := newTestSvc(t)
	_ = s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 5})
	_, err := s.RegisterCredit(CreditInput{HolderID: "h", Category: Required, Credits: 1, EarnedOn: 1, Org: "o", Now: 4})
	mustErr(t, err, ErrClockRollback)
	// 被拒绝操作不得推进时钟：now=5 的操作仍被接受。
	if _, err := s.RegisterCredit(CreditInput{HolderID: "h", Category: Required, Credits: 1, EarnedOn: 5, Org: "o", Now: 5}); err != nil {
		t.Fatalf("state after rejected op must be unchanged: %v", err)
	}
}
