package reconcile

import (
	"errors"
	"fmt"
	"testing"
)

// 已有效匹配的行不被后来的 Reconcile 改动。
func TestMatchedRowsStable(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k", 100, 10, "R")
	first, _ := m.Reconcile(0)
	logf(t, "首次输出 %+v", first)

	addb(t, m, "b2", 100, 10, "R")
	addk(t, m, "k2", 100, 10, "R")
	second, _ := m.Reconcile(0)
	logf(t, "新增行后输出 %+v; 判定: mid1 不动, 新行自成 mid2", second)
	assertMatches(t, first, []Match{{Mid: 1, Round: 1, BankID: []string{"b"}, BookID: []string{"k"}}})
	assertMatches(t, second, []Match{{Mid: 2, Round: 1, BankID: []string{"b2"}, BookID: []string{"k2"}}})
	if len(m.Matches()) != 2 {
		t.Fatalf("matches=%v", m.Matches())
	}
}

// Reverse 后禁配使原组合在第一至四轮都不再出现；
// 一对多 / 多对一中只有一个组合被禁时集合缩小而不再满足金额和。
func TestReverseForbiddenAllRounds(t *testing.T) {
	// 第一、二轮一对一禁配。
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k", 100, 10, "R")
	got, _ := m.Reconcile(0)
	assertMatches(t, got, []Match{{Mid: 1, Round: 1, BankID: []string{"b"}, BookID: []string{"k"}}})
	if _, _, err := m.Reverse(1); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Reconcile(0)
	logf(t, "一对一禁配后输出 %+v F=%v; 判定: 一二轮均不可再配", got, m.Forbidden())
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	// 第三轮整组禁配。
	m2 := NewMatcher()
	addb(t, m2, "b", 300, 20, "Y")
	addk(t, m2, "k1", 100, 20, "Y")
	addk(t, m2, "k2", 200, 20, "Y")
	got, _ = m2.Reconcile(0)
	logf(t, "一对多首次输出 %+v; 判定: S={k1,k2} 和=300", got)
	assertMatches(t, got, []Match{{Mid: 1, Round: 3, BankID: []string{"b"}, BookID: []string{"k1", "k2"}}})
	if _, _, err := m2.Reverse(1); err != nil {
		t.Fatal(err)
	}
	got, _ = m2.Reconcile(0)
	logf(t, "一对多整组禁配后输出 %+v; 判定: S 为空", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	// 第三轮部分禁配：b(100) 与 k1(100) 一对一撤销后 (b,k1) 入 F；
	// 再加 k2=60、k3=30。S 排除 k1 后为 {k2,k3}，和 90 != 100，不配。
	m3 := NewMatcher()
	addb(t, m3, "b", 100, 20, "Y")
	addk(t, m3, "k1", 100, 20, "Y")
	got, _ = m3.Reconcile(0)
	assertMatches(t, got, []Match{{Mid: 1, Round: 1, BankID: []string{"b"}, BookID: []string{"k1"}}})
	if _, _, err := m3.Reverse(1); err != nil {
		t.Fatal(err)
	}
	addk(t, m3, "k2", 60, 20, "Y")
	addk(t, m3, "k3", 30, 20, "Y")
	got, _ = m3.Reconcile(0)
	logf(t, "第三轮部分禁配输出 %+v F=%v; 判定: S 缩成 {k2,k3} 和=90 不配", got, m3.Forbidden())
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	// 第四轮部分禁配：s1(100) 与 t1(100) 撤销后 (t1,s1) 入 F；
	// 再加 t2=60、t3=30。T 排除 t1 后和 90 != 100，不配。
	m4 := NewMatcher()
	addb(t, m4, "t1", 100, 40, "Z")
	addk(t, m4, "s1", 100, 40, "Z")
	got, _ = m4.Reconcile(0)
	assertMatches(t, got, []Match{{Mid: 1, Round: 1, BankID: []string{"t1"}, BookID: []string{"s1"}}})
	if _, _, err := m4.Reverse(1); err != nil {
		t.Fatal(err)
	}
	addb(t, m4, "t2", 60, 40, "Z")
	addb(t, m4, "t3", 30, 40, "Z")
	got, _ = m4.Reconcile(0)
	logf(t, "第四轮部分禁配输出 %+v F=%v; 判定: T 缩成 {t2,t3} 和=90 不配", got, m4.Forbidden())
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	// 第四轮整组禁配。
	m5 := NewMatcher()
	addb(t, m5, "t1", 100, 39, "Z")
	addb(t, m5, "t2", 200, 41, "Z")
	addk(t, m5, "s1", 300, 40, "Z")
	got, _ = m5.Reconcile(2)
	assertMatches(t, got, []Match{{Mid: 1, Round: 4, BankID: []string{"t1", "t2"}, BookID: []string{"s1"}}})
	if _, _, err := m5.Reverse(1); err != nil {
		t.Fatal(err)
	}
	got, _ = m5.Reconcile(2)
	logf(t, "多对一整组禁配后输出 %+v; 判定: T 为空", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 撤销后 mid 不回收；Matches 按银行最小 id 再按 mid 排序。
func TestReverseMidNotReusedAndOrdering(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b1", 100, 10, "R")
	addk(t, m, "k1", 100, 10, "R")
	got, _ := m.Reconcile(0)
	if got[0].Mid != 1 {
		t.Fatalf("mid=%d", got[0].Mid)
	}
	if _, _, err := m.Reverse(1); err != nil {
		t.Fatal(err)
	}
	addb(t, m, "b2", 200, 10, "Q")
	addk(t, m, "k2", 200, 10, "Q")
	got, _ = m.Reconcile(0)
	logf(t, "撤销后新配对输出 %+v; 判定: mid=2 而非回收 1", got)
	assertMatches(t, got, []Match{{Mid: 2, Round: 1, BankID: []string{"b2"}, BookID: []string{"k2"}}})

	if _, _, err := m.Reverse(2); err != nil {
		t.Fatal(err)
	}
	addb(t, m, "z1", 20, 0, "B")
	addk(t, m, "zk1", 20, 0, "B")
	got, _ = m.Reconcile(0)
	logf(t, "先产生 z1 配对 %+v; 判定: mid=3", got)
	if len(got) != 1 || got[0].Mid != 3 {
		t.Fatalf("got %+v", got)
	}
	addb(t, m, "a0", 10, 0, "A")
	addk(t, m, "ak0", 10, 0, "A")
	got, _ = m.Reconcile(0)
	logf(t, "再产生 a0 配对 %+v; 判定: mid=4", got)
	if len(got) != 1 || got[0].Mid != 4 {
		t.Fatalf("got %+v", got)
	}
	all := m.Matches()
	logf(t, "Matches 视图 %+v; 判定: 虽 mid4 产生更晚, 但银行最小 id a0 排在 z1 前", all)
	if all[0].BankID[0] != "a0" || all[0].Mid != 4 || all[1].BankID[0] != "z1" || all[1].Mid != 3 {
		t.Fatalf("ordering %+v", all)
	}
}

// Reverse 对已撤销与从未产生的 mid 区分错误。
func TestReverseErrorKinds(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k", 100, 10, "R")
	got, _ := m.Reconcile(0)

	if _, _, err := m.Reverse(999); !errors.Is(err, ErrMatchNotFound) {
		logf(t, "Reverse(999) 错误 %v; 判定: 从未产生 -> ErrMatchNotFound", err)
		t.Fatalf("err=%v", err)
	}
	if _, _, err := m.Reverse(1); err != nil {
		t.Fatal(err)
	}
	_, _, errRev := m.Reverse(got[0].Mid)
	logf(t, "再次 Reverse(1) 错误 %v; 判定: 已撤销 -> ErrMatchReversed", errRev)
	if !errors.Is(errRev, ErrMatchReversed) {
		t.Fatalf("err=%v", errRev)
	}
	if _, _, err := m.Reverse(12345); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("never-created err=%v", err)
	}
}

// AddLine 错误原因顺序与被拒绝不改状态。
func TestAddLineValidation(t *testing.T) {
	m := NewMatcher()

	if err := m.AddLine(Side(99), "x", 1, 0, ""); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("side err=%v", err)
	}
	for _, tc := range []struct {
		name string
		id   string
		amt  int64
		day  int64
	}{
		{"empty id", "", 1, 0},
		{"zero amt", "a", 0, 0},
		{"amt too big", "a", maxAmt + 1, 0},
		{"amt too small", "a", -maxAmt - 1, 0},
		{"day neg", "a", 1, -1},
		{"day too big", "a", 1, maxDay + 1},
	} {
		err := m.AddLine(Bank, tc.id, tc.amt, tc.day, "")
		logf(t, "非法行[%s] 错误 %v; 判定: ErrInvalidLine", tc.name, err)
		if !errors.Is(err, ErrInvalidLine) {
			t.Fatalf("%s: err=%v", tc.name, err)
		}
	}

	if err := m.AddLine(Bank, "dup", 1, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.AddLine(Bank, "dup", 1, 0, ""); !errors.Is(err, ErrDuplicateID) {
		logf(t, "重复 id 错误 %v; 判定: ErrDuplicateID", err)
		t.Fatalf("err=%v", err)
	}

	if u := unmatchedErr(t, m, Bank); len(u) != 1 || u[0].ID != "dup" {
		t.Fatalf("bank=%v", u)
	}
	if len(unmatchedErr(t, m, Book)) != 0 {
		t.Fatal("book mutated")
	}

	if err := m.AddLine(Side(99), "", 0, -1, ""); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("priority side err=%v", err)
	}
	if err := m.AddLine(Bank, "dup", 0, 0, ""); !errors.Is(err, ErrInvalidLine) {
		t.Fatalf("priority line err=%v", err)
	}
	if _, err := m.Unmatched(Side(99)); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("unmatched side err=%v", err)
	}

	if _, err := m.Reconcile(-1); !errors.Is(err, ErrInvalidW) {
		t.Fatalf("w=-1 err=%v", err)
	}
	if _, err := m.Reconcile(maxW + 1); !errors.Is(err, ErrInvalidW) {
		t.Fatalf("w big err=%v", err)
	}
	if len(m.Matches()) != 0 {
		t.Fatal("state mutated by rejected reconcile")
	}
}

// 满额（每侧 1e5）报行非法，两侧计数独立。
func TestCapacityFull(t *testing.T) {
	m := NewMatcher()
	for i := 0; i < maxLines; i++ {
		if err := m.AddLine(Bank, fmt.Sprintf("b%06d", i), 1, 0, ""); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	err := m.AddLine(Bank, "zzz-overflow", 1, 0, "")
	logf(t, "满额后再登记错误 %v; 判定: ErrInvalidLine(side full)", err)
	if !errors.Is(err, ErrInvalidLine) {
		t.Fatalf("err=%v", err)
	}
	if err := m.AddLine(Book, "k0", 1, 0, ""); err != nil {
		t.Fatalf("book side independent: %v", err)
	}
}
