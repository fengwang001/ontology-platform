package review

import "testing"

func TestVerdictBook(t *testing.T) {
	s := NewStore()
	// 每人只留最近一次非 Comment 裁决（Comment 在 gate 层过滤，账层只接收非 Comment）。
	s.SetVerdict(1, "u", Approve)
	if v, ok := s.Verdict(1, "u"); !ok || v != Approve {
		t.Fatalf("got %v,%v", v, ok)
	}
	s.SetVerdict(1, "u", RequestChanges)
	if v, _ := s.Verdict(1, "u"); v != RequestChanges {
		t.Fatalf("latest verdict = %v", v)
	}
	// PR 隔离。
	if _, ok := s.Verdict(2, "u"); ok {
		t.Fatal("verdict leaked across PRs")
	}
	// ClearApprovals 只清批准。
	s.SetVerdict(1, "a", Approve)
	s.SetVerdict(1, "r", RequestChanges)
	s.ClearApprovals(1)
	if _, ok := s.Verdict(1, "a"); ok {
		t.Fatal("approval not cleared")
	}
	if v, _ := s.Verdict(1, "r"); v != RequestChanges {
		t.Fatal("request-changes wrongly cleared")
	}
	if s.ClearVerdict(1, "r") != true {
		t.Fatal("clear existing verdict")
	}
	if s.ClearVerdict(1, "r") != false {
		t.Fatal("clear absent verdict should report false")
	}
}

func TestCheckBook(t *testing.T) {
	s := NewStore()
	s.SetCheck(1, "build", 1, Pending)
	s.SetCheck(1, "build", 1, Failure)
	if st, ok := s.Check(1, "build", 1); !ok || st != Failure {
		t.Fatalf("last-write = %v,%v", st, ok)
	}
	// (check,head) 与 (pr) 维度独立。
	if st, _ := s.Check(1, "build", 2); st != 0 {
		t.Fatalf("different head leaked: %v", st)
	}
	if _, ok := s.Check(2, "build", 1); ok {
		t.Fatal("different PR leaked")
	}
}

func TestPermsAndActiveAtDecisionTime(t *testing.T) {
	s := NewStore()
	s.SetVerdict(1, "w", Approve)
	s.SetVerdict(1, "n", Approve)
	s.SetVerdict(1, "a", RequestChanges)
	s.SetLevel("w", Write)
	s.SetLevel("a", Admin)
	// n 未登记 = none。
	if got := len(s.ActiveVerdicts(1, Approve)); got != 1 {
		t.Fatalf("none approvals counted: %d", got)
	}
	s.SetLevel("n", Write)
	if got := len(s.ActiveVerdicts(1, Approve)); got != 2 {
		t.Fatalf("restored perm: %d", got)
	}
	if cr := s.ActiveVerdicts(1, RequestChanges); len(cr) != 1 {
		t.Fatalf("admin change request = %d", len(cr))
	}
}

// TestTouchedIndependentOfPRCount 证明单次 Review/Report 触碰记录 ≤2，且与 PR 总数无关。
func TestTouchedIndependentOfPRCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := NewStore()
		for pr := 1; pr <= n; pr++ {
			s.SetVerdict(pr, "u1", Approve)
			s.SetCheck(pr, "build", 1, Success)
		}
		s.Touched = 0

		s.SetVerdict(7, "u2", RequestChanges) // 1 条写
		if s.Touched > 2 {
			t.Fatalf("n=%d: Review touched %d records", n, s.Touched)
		}
		s.Touched = 0
		s.SetCheck(7, "build", 1, Failure) // 1 条写
		if s.Touched > 2 {
			t.Fatalf("n=%d: Report touched %d records", n, s.Touched)
		}
		s.Touched = 0
		if ok := s.ClearVerdict(7, "u2"); !ok || s.Touched > 2 {
			t.Fatalf("n=%d: Dismiss touched %d records (%v)", n, s.Touched, ok)
		}
		t.Logf("n=%d: per-op touched records = 1, independent of PR total", n)
	}
}
