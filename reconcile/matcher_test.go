package reconcile

import "testing"

// 规格中的完整例子：四轮各得其所。
func TestExampleFromSpec(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b1", 100, 10, "X")
	addb(t, m, "b2", 100, 12, "X")
	addb(t, m, "b3", 300, 20, "Y")
	addb(t, m, "b4", 50, 30, "")
	addb(t, m, "b5", 50, 31, "")
	addk(t, m, "k1", 100, 11, "X")
	addk(t, m, "k2", 100, 13, "X")
	addk(t, m, "k3", 100, 19, "Y")
	addk(t, m, "k4", 200, 21, "Y")
	addk(t, m, "k5", 50, 31, "")

	got, err := m.Reconcile(2)
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Mid: 1, Round: 1, BankID: []string{"b1"}, BookID: []string{"k1"}},
		{Mid: 2, Round: 1, BankID: []string{"b2"}, BookID: []string{"k2"}},
		{Mid: 3, Round: 2, BankID: []string{"b4"}, BookID: []string{"k5"}},
		{Mid: 4, Round: 3, BankID: []string{"b3"}, BookID: []string{"k3", "k4"}},
	}
	for i := range got {
		logf(t, "输入: 10行 W=2; 输出第%d条 mid=%d round=%d bank=%v book=%v; 判定: 期望 mid=%d round=%d",
			i+1, got[i].Mid, got[i].Round, got[i].BankID, got[i].BookID, want[i].Mid, want[i].Round)
	}
	assertMatches(t, got, want)
	if u := unmatchedErr(t, m, Bank); len(u) != 1 || u[0].ID != "b5" {
		t.Fatalf("unmatched bank=%v", u)
	}
	if len(unmatchedErr(t, m, Book)) != 0 {
		t.Fatalf("unmatched book=%v", unmatchedErr(t, m, Book))
	}
}

// 规格中的多对一 + 撤销 + 禁配 + 新行例子。
func TestManyToOneReverseForbidden(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "t1", 100, 39, "Z")
	addb(t, m, "t2", 200, 41, "Z")
	addk(t, m, "s1", 300, 40, "Z")

	got, err := m.Reconcile(2)
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "输入 t1/t2/s1 W=2; 输出 %+v; 判定: 第四轮 T={t1,t2} 和=300", got)
	assertMatches(t, got, []Match{{Mid: 1, Round: 4, BankID: []string{"t1", "t2"}, BookID: []string{"s1"}}})

	banks, books, err := m.Reverse(1)
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "Reverse(1) 输出 banks=%v books=%v F=%v; 判定: 两组合入禁配, mid 不回收", banks, books, m.Forbidden())
	if len(banks) != 2 || len(books) != 1 || len(m.Forbidden()) != 2 {
		t.Fatalf("banks=%v books=%v F=%v", banks, books, m.Forbidden())
	}

	got, _ = m.Reconcile(2)
	logf(t, "禁配后再对账输出 %+v; 判定: T 中两组合均在 F, T 为空", got)
	if len(got) != 0 {
		t.Fatalf("forbidden combo rematched: %+v", got)
	}

	addb(t, m, "t3", 300, 40, "Z")
	got, _ = m.Reconcile(2)
	logf(t, "登记 t3 后输出 %+v; 判定: t3/s1 不在 F, 第一轮配, mid=2", got)
	assertMatches(t, got, []Match{{Mid: 2, Round: 1, BankID: []string{"t3"}, BookID: []string{"s1"}}})
	u := unmatchedErr(t, m, Bank)
	if len(u) != 2 || u[0].ID != "t1" || u[1].ID != "t2" {
		t.Fatalf("unmatched banks=%v", u)
	}
}

// 日期差恰等于 W 在内，W+1 被排除（正负两侧）。
func TestDayBoundary(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "keq", 100, 13, "R")
	addk(t, m, "kover", 100, 14, "R")
	got, _ := m.Reconcile(3)
	logf(t, "W=3 正向: 输出 %+v; 判定: |diff|=3 含, =4 不含", got)
	if len(got) != 1 || got[0].BookID[0] != "keq" {
		t.Fatalf("got %+v", got)
	}

	m2 := NewMatcher()
	addb(t, m2, "b", 100, 10, "R")
	addk(t, m2, "keq", 100, 7, "R")
	addk(t, m2, "kover", 100, 6, "R")
	got, _ = m2.Reconcile(3)
	logf(t, "W=3 负向: 输出 %+v; 判定: |diff|=3 含", got)
	if len(got) != 1 || got[0].BookID[0] != "keq" {
		t.Fatalf("got %+v", got)
	}

	m3 := NewMatcher()
	addb(t, m3, "b", 100, 10, "R")
	addk(t, m3, "kover", 100, 14, "R")
	got, _ = m3.Reconcile(3)
	logf(t, "W+1: 输出 %+v; 判定: 不匹配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 同 ref 但金额不等不在第一轮配对。
func TestSameRefDifferentAmt(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k", 101, 10, "R")
	got, _ := m.Reconcile(2)
	logf(t, "输入 b=100 k=101 同ref; 输出 %+v; 判定: 金额不等四轮均不配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 第一轮优先于第二轮。
func TestRound1Precedence(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "kref", 100, 12, "R")
	addk(t, m, "knoref", 100, 10, "S")
	got, _ := m.Reconcile(2)
	logf(t, "输出 %+v; 判定: 第一轮同ref diff=2 优先于第二轮异ref diff=0", got)
	if len(got) != 1 || got[0].Round != 1 || got[0].BookID[0] != "kref" {
		t.Fatalf("got %+v", got)
	}
}

// 并列日期差取 id 字节序最小。
func TestTieBreakByID(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k2", 100, 11, "R")
	addk(t, m, "k1", 100, 11, "R")
	got, _ := m.Reconcile(2)
	logf(t, "输出 %+v; 判定: diff 同为1, 取 id 最小 k1", got)
	if got[0].BookID[0] != "k1" {
		t.Fatalf("got %+v", got)
	}
}

// 正负金额互不相等。
func TestSignMismatch(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 100, 10, "R")
	addk(t, m, "k", -100, 10, "R")
	got, _ := m.Reconcile(0)
	logf(t, "输出 %+v; 判定: +100 != -100", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 先处理的银行行优先，不做全局最优：b4 先配走 k5。
func TestGreedyNoGlobalOptimum(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b4", 50, 30, "")
	addb(t, m, "b5", 50, 31, "")
	addk(t, m, "k5", 50, 31, "")
	got, _ := m.Reconcile(2)
	logf(t, "输出 %+v; 判定: b4 先处理配走 k5, b5 无配", got)
	if len(got) != 1 || got[0].BankID[0] != "b4" {
		t.Fatalf("got %+v", got)
	}
}

// 一对多 / 多对一集合大小为 1 不匹配。
func TestGroupSizeOne(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 300, 20, "Y")
	addk(t, m, "k", 100, 20, "Y")
	got, _ := m.Reconcile(2)
	logf(t, "一对多 |S|=1: 输出 %+v; 判定: 不匹配, 不试子集", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	m2 := NewMatcher()
	addb(t, m2, "t1", 100, 40, "Z")
	addk(t, m2, "s1", 300, 40, "Z")
	got, _ = m2.Reconcile(2)
	logf(t, "多对一 |T|=1: 输出 %+v; 判定: 不匹配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// 分组金额和差 1 不匹配。
func TestGroupSumOffByOne(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 300, 20, "Y")
	addk(t, m, "k1", 100, 20, "Y")
	addk(t, m, "k2", 199, 20, "Y")
	got, _ := m.Reconcile(2)
	logf(t, "一对多和=299: 输出 %+v; 判定: 差1不配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}

	m2 := NewMatcher()
	addb(t, m2, "t1", 100, 39, "Z")
	addb(t, m2, "t2", 201, 41, "Z")
	addk(t, m2, "s1", 300, 40, "Z")
	got, _ = m2.Reconcile(2)
	logf(t, "多对一和=301: 输出 %+v; 判定: 差1不配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// ref 相同但日期超出 W 的行不入集合。
func TestGroupDateExclusion(t *testing.T) {
	m := NewMatcher()
	addb(t, m, "b", 300, 20, "Y")
	addk(t, m, "k1", 100, 20, "Y")
	addk(t, m, "k2", 200, 30, "Y")
	got, _ := m.Reconcile(2)
	logf(t, "输出 %+v; 判定: k2 diff=10 不入 S, 无匹配", got)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}
