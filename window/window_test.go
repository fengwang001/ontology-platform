package window

import "testing"

func TestRollAndEst(t *testing.T) {
	tb := NewTable()
	key := Key{RuleID: "r", Values: []string{"t1"}}
	for i := 0; i < 8; i++ {
		tb.AllowIncr(key, 0, 100, 11)
	}
	// now=150：k'=1=k+1，prev=8,cur=0，est=0+floor(8*50/100)=4
	if got := tb.Estimate(key, 150, 100); got != 4 {
		t.Fatalf("est at 150 = %d, want 4", got)
	}
	// 连续请求：est 4..9 放行 6 次（cur 到 6），第 7 次 est=10 超限。
	for want := int64(4); want <= 9; want++ {
		if got := tb.Estimate(key, 150, 100); got != want {
			t.Fatalf("before incr est = %d, want %d", got, want)
		}
		tb.AllowIncr(key, 150, 100, 11)
	}
	if got := tb.Estimate(key, 150, 100); got != 10 {
		t.Fatalf("7th est = %d, want 10 (reject)", got)
	}
	s, _ := tb.Get(key)
	if s.Cur != 6 || s.Prev != 8 {
		t.Fatalf("cur=%d prev=%d, want 6,8", s.Cur, s.Prev)
	}
	// now=199：est=6+floor(8*1/100)=6（向下取整为 0）
	if got := tb.Estimate(key, 199, 100); got != 6 {
		t.Fatalf("est at 199 = %d, want 6", got)
	}
}

func TestBoundaryFullPrev(t *testing.T) {
	// 窗口 1 内 cur=6：边界 now=200（mod W==0）上一窗口全额计入。
	tb := NewTable()
	key := Key{RuleID: "r", Values: []string{"t1"}}
	for i := 0; i < 6; i++ {
		tb.AllowIncr(key, 150, 100, 7) // 窗口 1 内 cur=6
	}
	if got := tb.Estimate(key, 200, 100); got != 6 {
		t.Fatalf("est at boundary = %d, want 6", got)
	}
	// k'=3 >= k+2（相对 k=1 跳过两个窗口）：两窗口都清零。
	if got := tb.Estimate(key, 300, 100); got != 0 {
		t.Fatalf("est after 2-window gap = %d, want 0", got)
	}
	// AllowIncr 在 k+2 后落地：prev=0,cur=1。
	tb.AllowIncr(key, 300, 100, 7)
	s, _ := tb.Get(key)
	if s.K != 3 || s.Cur != 1 || s.Prev != 0 {
		t.Fatalf("after gap k=%d cur=%d prev=%d, want 3,1,0", s.K, s.Cur, s.Prev)
	}
	// 交接后边界：prev=6,cur=1 在 now=200 时 est=1+6=7。
	key2 := Key{RuleID: "r2", Values: []string{"t1"}}
	for i := 0; i < 6; i++ {
		tb.AllowIncr(key2, 150, 100, 8)
	}
	tb.AllowIncr(key2, 200, 100, 8)
	if got := tb.Estimate(key2, 200, 100); got != 7 {
		t.Fatalf("est after handoff = %d, want 7", got)
	}
}

func TestIndependentBuckets(t *testing.T) {
	tb := NewTable()
	k1 := Key{RuleID: "r", Values: []string{"t1"}}
	k2 := Key{RuleID: "r", Values: []string{"t2"}}
	tb.AllowIncr(k1, 0, 100, 2)
	if tb.Len() != 1 {
		t.Fatalf("len=%d want 1", tb.Len())
	}
	tb.AllowIncr(k2, 0, 100, 2)
	if tb.Len() != 2 {
		t.Fatalf("len=%d want 2", tb.Len())
	}
	s1, _ := tb.Get(k1)
	s2, _ := tb.Get(k2)
	if s1.Cur != 1 || s2.Cur != 1 {
		t.Fatalf("buckets leaked: %+v %+v", s1, s2)
	}
}

func TestCapAndDelete(t *testing.T) {
	tb := NewTable()
	key := Key{RuleID: "r", Values: []string{"x"}}
	for i := 0; i < 5; i++ {
		tb.AllowIncr(key, 0, 100, 3) // 封顶 L+1=3
	}
	s, _ := tb.Get(key)
	if s.Cur != 3 || s.Prev != 0 {
		t.Fatalf("cur=%d prev=%d, want cap 3,0", s.Cur, s.Prev)
	}
	if n := tb.DeleteRule("r"); n != 1 || tb.Len() != 0 {
		t.Fatalf("delete n=%d len=%d", n, tb.Len())
	}
}
