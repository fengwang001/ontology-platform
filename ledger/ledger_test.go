package ledger

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"ontology/plan"
)

func mustNew(t *testing.T, wn int64) *Ledger {
	t.Helper()
	l, err := New(wn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustAdd(t *testing.T, l *Ledger, name string, bd int64) {
	t.Helper()
	if err := l.AddDataset(name, bd); err != nil {
		t.Fatalf("AddDataset %s: %v", name, err)
	}
}

func mustAddAnalyst(t *testing.T, l *Ledger, name string, ba int64) {
	t.Helper()
	if err := l.AddAnalyst(name, ba); err != nil {
		t.Fatalf("AddAnalyst %s: %v", name, err)
	}
}

func leaf(c int, parts ...int) *plan.Leaf { return &plan.Leaf{Cost: c, Parts: parts} }

func checkErr(t *testing.T, err, want error, step string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err=%v want=%v", step, err, want)
	}
}

// TestSpecWalkthrough 复现题目给出的完整数值样例。
func TestSpecWalkthrough(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "D", 1000)
	mustAddAnalyst(t, l, "a", 300)
	P := &plan.Seq{Children: []plan.Node{
		leaf(100, 1),
		&plan.Par{Children: []plan.Node{leaf(80, 2), leaf(120, 3)}},
		&plan.Sample{Num: 1, Den: 3, Child: leaf(100, 4)},
	}}

	checkErr(t, l.Reserve("q1", "a", []string{"D"}, P, 10), nil, "q1 reserve")
	if rem, _ := l.Remaining("D"); rem != 746 {
		t.Fatalf("after q1: dataset rem=%d want 746", rem)
	}
	if rem, _ := l.AnalystRemaining("a", 10); rem != 46 {
		t.Fatalf("after q1: analyst rem=%d want 46", rem)
	}
	checkErr(t, l.Reserve("q2", "a", []string{"D"}, leaf(46, 1), 20), nil, "q2 reserve exact")
	checkErr(t, l.Reserve("q3", "a", []string{"D"}, leaf(1, 1), 30), ErrAnalystExhausted, "q3 rejected")

	checkErr(t, l.Start("q1", 40), nil, "start q1")
	checkErr(t, l.Commit("q1", 200, 50), nil, "commit q1")
	if rem, _ := l.Remaining("D"); rem != 1000-200-46 {
		t.Fatalf("after commit: rem=%d want 754", rem)
	}
	if rem, _ := l.AnalystRemaining("a", 50); rem != 54 {
		t.Fatalf("window0 after commit rem=%d want 54", rem)
	}

	checkErr(t, l.Reserve("q3", "a", []string{"D"}, leaf(54, 1), 60), nil, "q3 exact full reserve")
	if rem, _ := l.Remaining("D"); rem != 700 {
		t.Fatalf("after q3 reserve: rem=%d want 700", rem)
	}
	checkErr(t, l.Reserve("q4", "a", []string{"D"}, leaf(300, 1), 130), nil, "q4 new window")
	if rem, _ := l.AnalystRemaining("a", 130); rem != 0 {
		t.Fatalf("window1 rem=%d want 0", rem)
	}
	checkErr(t, l.Cancel("q2", 140), nil, "cancel q2 reserved")
	// 此时 used=200，resv=354（q3 54 + q4 300），故 Remaining=446。
	if rem, _ := l.Remaining("D"); rem != 446 {
		t.Fatalf("after cancel q2: rem=%d want 446", rem)
	}
	checkErr(t, l.Start("q4", 145), nil, "start q4")
	checkErr(t, l.Cancel("q4", 150), nil, "cancel q4 running")
	if rem, _ := l.Remaining("D"); rem != 446 {
		t.Fatalf("final Remaining(D)=%d want 446", rem)
	}
	if rem, _ := l.AnalystRemaining("a", 150); rem != 0 {
		t.Fatalf("window1 after running cancel rem=%d want 0", rem)
	}
}

func TestReserveRejectOrder(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "d1", 10)
	mustAddAnalyst(t, l, "a1", 10)

	checkErr(t, l.Reserve("", "a1", []string{"d1"}, leaf(1, 1), 0), ErrInvalidArgument, "empty qid")
	checkErr(t, l.Reserve("x", "a1", []string{"d1"}, leaf(1), 0), ErrInvalidArgument, "leaf without parts")
	checkErr(t, l.Reserve("x", "a1", []string{"d1"}, leaf(1, 1), -1), ErrInvalidArgument, "negative now")
	checkErr(t, l.Reserve("x", "a1", nil, leaf(1, 1), 0), ErrInvalidArgument, "no datasets")
	checkErr(t, l.Reserve("x", "a1", []string{"d1", "d1"}, leaf(1, 1), 0), ErrInvalidArgument, "duplicate datasets")

	if err := l.Reserve("q", "a1", []string{"d1"}, leaf(5, 1), 5); err != nil {
		t.Fatalf("setup reserve: %v", err)
	}
	checkErr(t, l.Reserve("q", "a1", []string{"d1"}, leaf(1, 1), 4), ErrClockRewind, "clock rewind before duplicate")
	checkErr(t, l.Reserve("q", "a1", []string{"d1"}, leaf(1, 1), 5), ErrDuplicateQID, "duplicate qid")
	checkErr(t, l.Reserve("r", "zz", []string{"zz"}, leaf(1, 1), 6), ErrUnknownDataset, "unknown dataset first")
	checkErr(t, l.Reserve("r", "zz", []string{"d1"}, leaf(1, 1), 6), ErrUnknownAnalyst, "unknown analyst")

	overlap := &plan.Par{Children: []plan.Node{leaf(100, 1), leaf(100, 1)}}
	checkErr(t, l.Reserve("r", "a1", []string{"d1"}, overlap, 6), ErrNotDisjoint, "not disjoint before exhausted")
	checkErr(t, l.Reserve("r", "a1", []string{"d1"}, leaf(6, 1), 6), ErrDatasetExhausted, "dataset exhausted")
}

func TestMultiDatasetExhaustedOrderAndAtomic(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "z", 100)
	mustAdd(t, l, "m", 10)
	mustAdd(t, l, "a_ds", 50)
	mustAddAnalyst(t, l, "an", 1000)

	// cost 20：字节序 a_ds(剩50) 够、m(剩10) 不足，报第一个不足者 m。
	err := l.Reserve("q1", "an", []string{"z", "m", "a_ds"}, leaf(20, 1), 0)
	checkErr(t, err, ErrDatasetExhausted, "m is the lexicographically-first exhausted dataset")
	for _, name := range []string{"z", "m", "a_ds"} {
		initial := int64(0)
		switch name {
		case "z":
			initial = 100
		case "m":
			initial = 10
		case "a_ds":
			initial = 50
		}
		if rem, _ := l.Remaining(name); rem != initial {
			t.Errorf("dataset %s charged after rejection: rem=%d want %d", name, rem, initial)
		}
	}
	if rem, _ := l.AnalystRemaining("an", 0); rem != 1000 {
		t.Errorf("analyst charged after rejection: rem=%d", rem)
	}
}

func TestStateTransitionsAndOverspend(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "d", 1000)
	mustAddAnalyst(t, l, "a", 1000)

	checkErr(t, l.Start("x", 0), ErrUnknownQuery, "start unknown")
	checkErr(t, l.Commit("x", 0, 0), ErrUnknownQuery, "commit unknown")
	checkErr(t, l.Cancel("x", 0), ErrUnknownQuery, "cancel unknown")

	if err := l.Reserve("q", "a", []string{"d"}, leaf(100, 1), 0); err != nil {
		t.Fatal(err)
	}
	checkErr(t, l.Commit("q", 0, 0), ErrWrongState, "commit reserved")
	if err := l.Cancel("q", 0); err != nil {
		t.Fatalf("cancel reserved: %v", err)
	}
	checkErr(t, l.Cancel("q", 0), ErrWrongState, "re-cancel cancelled")

	if err := l.Reserve("q2", "a", []string{"d"}, leaf(100, 1), 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Start("q2", 1); err != nil {
		t.Fatal(err)
	}
	checkErr(t, l.Start("q2", 1), ErrWrongState, "double start")
	checkErr(t, l.Reserve("q2", "a", []string{"d"}, leaf(1, 1), 1), ErrDuplicateQID, "reuse qid")
	checkErr(t, l.Commit("q2", 101, 2), ErrOverspend, "overspend")
	// overspend 拒绝不改状态：随后可以正常 commit。
	if err := l.Commit("q2", 100, 2); err != nil {
		t.Fatalf("commit after rejected overspend: %v", err)
	}
	checkErr(t, l.Start("q2", 3), ErrWrongState, "start committed")
	checkErr(t, l.Commit("q2", 0, 3), ErrWrongState, "commit committed")
	checkErr(t, l.Cancel("q2", 3), ErrWrongState, "cancel committed")
}

func TestExactEqualityAndWindows(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "d", 100)
	mustAddAnalyst(t, l, "a", 50)

	if err := l.Reserve("q", "a", []string{"d"}, leaf(50, 1), 50); err != nil {
		t.Fatalf("exact analyst fill: %v", err)
	}
	if err := l.Start("q", 99); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit("q", 10, 99); err != nil {
		t.Fatal(err)
	}
	if rem, _ := l.AnalystRemaining("a", 99); rem != 40 {
		t.Fatalf("old window refund: rem=%d want 40", rem)
	}
	if rem, _ := l.AnalystRemaining("a", 100); rem != 50 {
		t.Fatalf("new window free: rem=%d want 50", rem)
	}
	if _, err := l.AnalystRemaining("a", 98); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("readonly clock rewind: %v", err)
	}
}

func TestTouchedAndNodesCounters(t *testing.T) {
	for _, inflight := range []int{100, 10000} {
		name := "inflight" + strconv.Itoa(inflight)
		t.Run(name, func(t *testing.T) {
			l := mustNew(t, 1000)
			const huge = 1_000_000_000_000
			mustAdd(t, l, "d1", huge)
			mustAdd(t, l, "d2", huge)
			mustAddAnalyst(t, l, "a", huge)
			for i := 0; i < inflight; i++ {
				qid := "bg" + strconv.Itoa(i)
				if err := l.Reserve(qid, "a", []string{"d1"}, leaf(1, 1), 0); err != nil {
					t.Fatalf("bg reserve %d: %v", i, err)
				}
			}
			p := &plan.Seq{Children: []plan.Node{
				leaf(10, 1),
				&plan.Par{Children: []plan.Node{leaf(8, 2), leaf(9, 3)}},
				&plan.Sample{Num: 1, Den: 3, Child: leaf(10, 4)},
			}}
			if err := l.Reserve("target", "a", []string{"d1", "d2"}, p, 1); err != nil {
				t.Fatalf("target reserve: %v", err)
			}
			if l.touched != 4 {
				t.Errorf("touched=%d want 4 (2 datasets + analyst window + query)", l.touched)
			}
			if l.nodes != 7 {
				t.Errorf("nodes=%d want 7", l.nodes)
			}
			if err := l.Start("target", 2); err != nil {
				t.Fatal(err)
			}
			if err := l.Commit("target", 3, 3); err != nil {
				t.Fatal(err)
			}
			if l.touched != 4 {
				t.Errorf("commit touched=%d want 4", l.touched)
			}
		})
	}
}

func TestConcurrentReservesNeverExceedBudget(t *testing.T) {
	l := mustNew(t, 100)
	mustAdd(t, l, "d", 1000)
	for i := 0; i < 16; i++ {
		mustAddAnalyst(t, l, "a"+strconv.Itoa(i), 1000)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func(w, i int) {
				defer wg.Done()
				qid := "w" + strconv.Itoa(w) + "-" + strconv.Itoa(i)
				an := "a" + strconv.Itoa(w*2+(i%2))
				// 全部使用同一时刻 0，验证串行化下恰好接受能容纳的数量。
				_ = l.Reserve(qid, an, []string{"d"}, leaf(7, 1), 0)
			}(w, i)
		}
	}
	wg.Wait()
	if rem, _ := l.Remaining("d"); rem < 0 || rem > 1000 {
		t.Fatalf("dataset invariant broken: rem=%d", rem)
	}
	// used 恒为 0（均未结算），resv = 1000-rem。
	if rem, _ := l.Remaining("d"); (1000-rem)%7 != 0 {
		t.Fatalf("accepted reserve total inconsistent: rem=%d", rem)
	}
	for i := 0; i < 16; i++ {
		rem, err := l.AnalystRemaining("a"+strconv.Itoa(i), 0)
		if err != nil {
			t.Fatal(err)
		}
		if rem < 0 || rem > 1000 {
			t.Fatalf("analyst a%d window over limit: rem=%d", i, rem)
		}
	}
}
