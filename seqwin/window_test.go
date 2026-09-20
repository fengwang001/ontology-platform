package seqwin

import "testing"

// 语义 1：序列号 0 一律 Invalid，且不改变窗口状态。
func TestZeroIsInvalidAndStateless(t *testing.T) {
	w := New(8)
	if got := w.Accept(0); got != Invalid {
		t.Fatalf("Accept(0) = %v, want Invalid", got)
	}
	if w.Highest() != 0 {
		t.Fatalf("Highest after 0 = %d, want 0", w.Highest())
	}
	if w.Seen(0) {
		t.Fatal("Seen(0) = true, want false")
	}
	// 先收一个合法序列号，再收 0，状态同样不得变化。
	w.Accept(5)
	if got := w.Accept(0); got != Invalid {
		t.Fatalf("Accept(0) = %v, want Invalid", got)
	}
	if w.Highest() != 5 {
		t.Fatalf("Highest changed to %d after 0, want 5", w.Highest())
	}
}

// 语义 2：窗口右推，被推出左边界的旧序列号判 TooOld。
func TestWindowPushesRightAndEvicts(t *testing.T) {
	w := New(3)
	if got := w.Accept(10); got != Fresh {
		t.Fatalf("Accept(10) = %v, want Fresh", got)
	}
	if got := w.Accept(11); got != Fresh {
		t.Fatalf("Accept(11) = %v, want Fresh", got)
	}
	if w.Highest() != 11 {
		t.Fatalf("Highest = %d, want 11", w.Highest())
	}
	if got := w.Accept(8); got != TooOld {
		t.Fatalf("evicted 8 = %v, want TooOld", got)
	}
	if w.Seen(8) {
		t.Fatal("Seen(8) = true after eviction, want false")
	}
}

// 语义 3：乱序但在窗口内的未见过序列号判 Fresh，再来判 Duplicate。
func TestOutOfOrderWithinWindow(t *testing.T) {
	w := New(8)
	w.Accept(100)
	if got := w.Accept(97); got != Fresh {
		t.Fatalf("Accept(97) = %v, want Fresh", got)
	}
	if !w.Seen(97) {
		t.Fatal("Seen(97) = false, want true")
	}
	if got := w.Accept(97); got != Duplicate {
		t.Fatalf("Accept(97) again = %v, want Duplicate", got)
	}
}

// 语义 4：边界精确，闭区间 [Highest-size+1, Highest]。
// size=8、Highest=100 时 93 在窗内、92 在窗外。
func TestExactBoundary(t *testing.T) {
	w := New(8)
	w.Accept(100)
	if got := w.Accept(93); got != Fresh {
		t.Fatalf("Accept(93) = %v, want Fresh (left edge inside)", got)
	}
	if got := w.Accept(93); got != Duplicate {
		t.Fatalf("Accept(93) again = %v, want Duplicate", got)
	}
	if got := w.Accept(92); got != TooOld {
		t.Fatalf("Accept(92) = %v, want TooOld (just outside left edge)", got)
	}
}

// 语义 5：一次大跳不得漏判；清空时不能把新的最大值也清掉。
func TestBigJump(t *testing.T) {
	w := New(8)
	w.Accept(10)
	if got := w.Accept(10000); got != Fresh {
		t.Fatalf("Accept(10000) = %v, want Fresh", got)
	}
	if !w.Seen(10000) {
		t.Fatal("Seen(10000) = false after jump, want true")
	}
	if got := w.Accept(10000); got != Duplicate {
		t.Fatalf("Accept(10000) again = %v, want Duplicate", got)
	}
	if got := w.Accept(9999); got != Fresh {
		t.Fatalf("Accept(9999) = %v, want Fresh", got)
	}
	if got := w.Accept(10); got != TooOld {
		t.Fatalf("Accept(10) = %v, want TooOld", got)
	}
}

// 语义 6：重复的最大值判 Duplicate，Highest 不变。
func TestDuplicateHighest(t *testing.T) {
	w := New(4)
	w.Accept(42)
	if got := w.Accept(42); got != Duplicate {
		t.Fatalf("Accept(42) = %v, want Duplicate", got)
	}
	if got := w.Accept(42); got != Duplicate {
		t.Fatalf("Accept(42) third time = %v, want Duplicate", got)
	}
	if w.Highest() != 42 {
		t.Fatalf("Highest = %d, want 42", w.Highest())
	}
}

// 语义 7：内部位图长度只与 size 有关，不随序列号增长。
func TestBitmapCapacityBoundedBySize(t *testing.T) {
	const size = 16
	w := New(size)
	for seq := uint64(1); seq <= 100000; seq++ {
		if got := w.Accept(seq); got != Fresh {
			t.Fatalf("Accept(%d) = %v, want Fresh", seq, got)
		}
	}
	if got := w.BitmapLen(); got != size {
		t.Fatalf("bitmapLen = %d after 100k seqs, want %d", got, size)
	}
	if w.Highest() != 100000 {
		t.Fatalf("Highest = %d, want 100000", w.Highest())
	}
}

// New 的非正宽度统一视为 1。
func TestNewNonPositiveSizeBecomesOne(t *testing.T) {
	for _, size := range []int{0, -5} {
		w := New(size)
		if got := w.BitmapLen(); got != 1 {
			t.Fatalf("New(%d) bitmapLen = %d, want 1", size, got)
		}
		w.Accept(1)
		if got := w.Accept(1); got != Duplicate {
			t.Fatalf("New(%d): Accept(1) again = %v, want Duplicate", size, got)
		}
	}
}
