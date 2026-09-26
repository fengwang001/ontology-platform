package repl

import (
	"math/rand"
	"testing"
)

// naiveCommit 朴素重算：扫描 1..Len，找满足「当前任期 + 多数派」的最大 i，否则为 prev。
func naiveCommit(l *Leader, prev int) int {
	best := -1
	for i := 1; i <= l.lg.Len(); i++ {
		if l.lg.Term(i) != l.term {
			continue
		}
		cnt := 1
		for f := 2; f <= l.n; f++ {
			if l.matchIndex[f] >= i {
				cnt++
			}
		}
		if cnt >= l.n/2+1 {
			best = i
		}
	}
	if best < 0 {
		return prev
	}
	return best
}

// drive 对 l 施加 seed 决定的随机操作序列，每步后回调 visit。
func drive(l *Leader, seed int64, steps int, visit func(step int)) {
	r := rand.New(rand.NewSource(seed))
	term := l.term
	for s := 0; s < steps; s++ {
		switch r.Intn(3) {
		case 0:
			_ = l.Append(term)
		case 1:
			f := 2
			if l.n > 1 {
				f += r.Intn(l.n - 1)
			}
			_ = l.Replicate(f, r.Intn(2) == 0, r.Intn(l.lg.Len()+1))
		case 2:
			term++
			_ = l.Elect(term)
		}
		visit(s)
	}
}

// 不变量 1：任意操作序列后 CommitIndex 等于朴素重算结果。
func TestNaiveAgreement(t *testing.T) {
	for _, n := range []int{1, 3, 5} {
		l := New(n, 1)
		prev := 0
		drive(l, int64(n), 500, func(step int) {
			ci := l.CommitIndex()
			if want := naiveCommit(l, prev); ci != want {
				t.Fatalf("n=%d step %d: CommitIndex=%d, naive=%d", n, step, ci, want)
			}
			prev = ci
		})
	}
}

// 不变量 2：簿记自洽，且 Replicate 成功后 nextIndex == matchIndex+1。
func TestBookkeepingBounds(t *testing.T) {
	l := New(5, 1)
	drive(l, 7, 500, func(step int) {
		for f := 2; f <= 5; f++ {
			if m, nx := l.MatchIndex(f), l.NextIndex(f); m < 0 || m > l.Len() || nx < 1 || nx > l.Len()+1 {
				t.Fatalf("step %d f=%d: match=%d next=%d len=%d", step, f, m, nx, l.Len())
			}
		}
	})
	l2 := New(3, 1, 1, 1, 1)
	if err := l2.Replicate(2, true, 2); err != nil {
		t.Fatal(err)
	}
	if got, want := l2.NextIndex(2), l2.MatchIndex(2)+1; got != want {
		t.Fatalf("nextIndex=%d, want matchIndex+1=%d", got, want)
	}
}

// 不变量 3：commitIndex 单调不减（含 Elect）。
func TestCommitIndexMonotonic(t *testing.T) {
	for _, n := range []int{1, 3, 5} {
		l := New(n, 1)
		prev := 0
		drive(l, int64(100+n), 500, func(step int) {
			if ci := l.CommitIndex(); ci < prev {
				t.Fatalf("n=%d step %d: commitIndex %d < %d", n, step, ci, prev)
			} else {
				prev = ci
			}
		})
	}
}

// 复杂度约束：日志全部复制到多数派后，CommitIndex 的日志读取次数
// 是不随 m 增长的小常数（<=2，且各档相同）。
func TestReadCounter(t *testing.T) {
	prevReads := -1
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		terms := make([]int, m)
		for i := range terms {
			terms[i] = 1
		}
		l := New(3, 1, terms...)
		if err := l.Replicate(2, true, m); err != nil {
			t.Fatal(err)
		}
		if err := l.Replicate(3, true, m); err != nil {
			t.Fatal(err)
		}
		if got := l.CommitIndex(); got != m {
			t.Fatalf("m=%d: commitIndex=%d", m, got)
		}
		if l.lastReads > 2 {
			t.Fatalf("m=%d: lastReads=%d 超过常数界", m, l.lastReads)
		}
		if prevReads >= 0 && l.lastReads != prevReads {
			t.Fatalf("lastReads 随规模变化: %d -> %d", prevReads, l.lastReads)
		}
		prevReads = l.lastReads
	}
	if !CheckCommitReads() {
		t.Fatal("CheckCommitReads = false")
	}
}
