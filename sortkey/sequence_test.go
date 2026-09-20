package sortkey

import (
	"errors"
	"fmt"
	"testing"
)

func values(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

func equalStrings(a, b []string) bool {
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

// TestInsertPositions 覆盖空序列、最前、最后、两键之间四种插入。
func TestInsertPositions(t *testing.T) {
	s := NewSequence(NewFractional(64))

	k1, err := s.Insert("", "", "middle")
	if err != nil {
		t.Fatal(err)
	}
	k0, err := s.Insert("", k1, "front")
	if err != nil || !(k0 < k1) {
		t.Fatalf("front key %q, err %v, want < %q", k0, err, k1)
	}
	k2, err := s.Insert(k1, "", "back")
	if err != nil || !(k1 < k2) {
		t.Fatalf("back key %q, err %v, want > %q", k2, err, k1)
	}
	k01, err := s.Insert(k0, k1, "between")
	if err != nil || !(k0 < k01 && k01 < k1) {
		t.Fatalf("between key %q, err %v, want in (%q, %q)", k01, err, k0, k1)
	}

	got := values(s.Snapshot())
	want := []string{"front", "between", "middle", "back"}
	if !equalStrings(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestInsertDeterministic 同一对邻居反复调用 Insert 的键生成结果一致
// （直接对生成器验证；序列会因并发定位规则改变邻居）。
func TestInsertDeterministic(t *testing.T) {
	gen := NewFractional(64)
	first, err := gen.Between("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		got, err := gen.Between("a", "b")
		if err != nil || got != first {
			t.Fatalf("run %d: got %q, err %v, want %q", i, got, err, first)
		}
	}
}

// TestGrowRebalanceContinue 完整循环：在同一间隙连续插入直到触发
// ErrNeedsRebalance，重排后验证相对顺序不变，然后继续插入。
func TestGrowRebalanceContinue(t *testing.T) {
	const limit = 12
	s := NewSequence(NewFractional(limit))

	// 先放一个右端哨兵，之后不断在它与序列头部之间插入，
	// 新值总是最小，因此键在同一间隙内不断变长。
	if _, err := s.Insert("", "", "sentry"); err != nil {
		t.Fatal(err)
	}
	inserts := 0
	for i := 0; ; i++ {
		entries := s.Snapshot()
		right := entries[0].Key
		_, err := s.Insert("", right, fmt.Sprintf("v%04d", i))
		if errors.Is(err, ErrNeedsRebalance) {
			break
		}
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		inserts++
	}
	if inserts < 20 {
		t.Fatalf("only %d inserts before rebalance, want dozens", inserts)
	}
	stats := s.Stats()
	if stats.Longest > limit || stats.Headroom != limit-stats.Longest {
		t.Fatalf("stats = %+v, limit %d", stats, limit)
	}
	t.Logf("inserted %d keys, longest %d/%d", inserts, stats.Longest, limit)

	before := values(s.Snapshot())
	if err := s.Rebalance(); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	after := s.Snapshot()
	if got := values(after); !equalStrings(got, before) {
		t.Fatalf("order changed by rebalance:\nbefore %v\nafter  %v", before, got)
	}
	// 重排后键应重新变短。
	if st := s.Stats(); st.Longest >= stats.Longest {
		t.Fatalf("longest key %d after rebalance, was %d", st.Longest, stats.Longest)
	}
	// 重排后继续在同一位置插入应恢复可用。
	for i := 0; i < 10; i++ {
		right := s.Snapshot()[0].Key
		if _, err := s.Insert("", right, fmt.Sprintf("w%04d", i)); err != nil {
			t.Fatalf("insert after rebalance %d: %v", i, err)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after rebalance: %v", err)
	}
}

// TestRebalancePreservesOrder 重排前后任意两个元素的先后关系完全一致。
func TestRebalancePreservesOrder(t *testing.T) {
	s := NewSequence(NewFractional(64))
	// 构造一批长短不一的键。
	if _, err := s.Insert("", "", "m"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		entries := s.Snapshot()
		if _, err := s.Insert("", entries[0].Key, fmt.Sprintf("f%02d", i)); err != nil {
			t.Fatal(err)
		}
		last := entries[len(entries)-1].Key
		if _, err := s.Insert(last, "", fmt.Sprintf("b%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	before := values(s.Snapshot())
	if err := s.Rebalance(); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	if got := values(after); !equalStrings(got, before) {
		t.Fatal("rebalance changed relative order")
	}
	// 重排后的键是等宽的短键。
	for _, e := range after {
		if len(e.Key) != len(after[0].Key) {
			t.Fatalf("rebalanced keys not even width: %q vs %q", e.Key, after[0].Key)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestStats 报告最长键长度、上限与余量。
func TestStats(t *testing.T) {
	s := NewSequence(NewFractional(10))
	if st := s.Stats(); st != (Stats{Longest: 0, Limit: 10, Headroom: 10}) {
		t.Fatalf("empty stats = %+v", st)
	}
	if _, err := s.Insert("", "", "a"); err != nil {
		t.Fatal(err)
	}
	st := s.Stats()
	if st.Longest != 1 || st.Limit != 10 || st.Headroom != 9 {
		t.Fatalf("stats = %+v", st)
	}
}

// TestSequenceRejectsBadInput 序列入口同样区分三类错误。
func TestSequenceRejectsBadInput(t *testing.T) {
	s := NewSequence(NewFractional(4))
	if _, err := s.Insert("b", "a", "x"); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("order err = %v", err)
	}
	var ikErr *InvalidKeyError
	if _, err := s.Insert("a!", "b", "x"); !errors.As(err, &ikErr) {
		t.Fatalf("charset err = %v", err)
	}
	// 长度上限：limit=4 时过紧的间隙 ("a","a001") 需要 6 位中点键，
	// 触发需要重排错误。
	if _, err := s.Insert("a", "a001", "x"); !errors.Is(err, ErrNeedsRebalance) {
		t.Fatalf("rebalance err = %v", err)
	}
}
