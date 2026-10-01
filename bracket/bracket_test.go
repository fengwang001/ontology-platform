package bracket

import (
	"errors"
	"fmt"
	"testing"
)

func getm(b *Bracket, r, i int) Match {
	for _, m := range b.Bracket() {
		if m.Round == r && m.Index == i {
			return m
		}
	}
	panic(fmt.Sprintf("match (%d,%d) not found", r, i))
}

func TestNewRejectsInvalidN(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 65, 100} {
		if _, err := New(n); !errors.Is(err, ErrSeedOutOfRange) {
			t.Fatalf("New(%d) err=%v, want ErrSeedOutOfRange", n, err)
		}
	}
}

func TestFoldedOrderFormula(t *testing.T) {
	cases := map[int][]int{
		2:  {1, 2},
		4:  {1, 4, 2, 3},
		8:  {1, 8, 4, 5, 2, 7, 3, 6},
		16: {1, 16, 8, 9, 4, 13, 5, 12, 2, 15, 7, 10, 3, 14, 6, 11},
	}
	for size, want := range cases {
		got := foldedOrder(size)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("foldedOrder(%d)=%v, want %v", size, got, want)
		}
	}
}

func TestFirstRoundAndByes(t *testing.T) {
	// N=2：唯一一场首轮 (1,1)=1 vs 2，无轮空。
	b, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	m := getm(b, 1, 1)
	if m.Left != 1 || m.Right != 2 || m.Kind != KindOpen || !m.Ready || m.Winner != 0 {
		t.Fatalf("N=2 (1,1)=%+v", m)
	}

	// N=3：(1,1)=1 vs 缺席 -> 种子1轮空；(1,2)=2 vs 3 就绪。
	b, _ = New(3)
	m = getm(b, 1, 1)
	if m.Left != 1 || m.Right != 0 || m.Kind != KindBye || m.Winner != 1 || m.Ready {
		t.Fatalf("N=3 (1,1)=%+v, want bye for seed 1", m)
	}
	if err := b.Report(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("report on bye err=%v, want ErrByeMatch", err)
	}
	if err := b.Correct(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("correct on bye err=%v, want ErrByeMatch", err)
	}
	m = getm(b, 1, 2)
	if m.Left != 2 || m.Right != 3 || m.Kind != KindOpen || !m.Ready {
		t.Fatalf("N=3 (1,2)=%+v", m)
	}

	// N=6, B=8, order=[1,8,4,5,2,7,3,6]：
	// (1,1)=1轮空，(1,2)=4vs5，(1,3)=2轮空，(1,4)=3vs6。
	b, _ = New(6)
	want := []struct {
		l, r, w int
		k       Kind
	}{
		{1, 0, 1, KindBye},
		{4, 5, 0, KindOpen},
		{2, 0, 2, KindBye},
		{3, 6, 0, KindOpen},
	}
	for idx, ww := range want {
		m := getm(b, 1, idx+1)
		if m.Left != ww.l || m.Right != ww.r || m.Winner != ww.w || m.Kind != ww.k {
			t.Fatalf("N=6 (1,%d)=%+v, want %+v", idx+1, m, ww)
		}
	}
	// 次轮：(2,1) 左位为种子1，等待 4vs5 胜者；(2,2) 左位为种子2，等待 3vs6 胜者。
	if r21 := getm(b, 2, 1); r21.Left != 1 || r21.Right != 0 || r21.Ready {
		t.Fatalf("N=6 (2,1)=%+v", r21)
	}
	if err := b.Report(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if r21 := getm(b, 2, 1); r21.Left != 1 || r21.Right != 4 || !r21.Ready || r21.Winner != 0 {
		t.Fatalf("N=6 (2,1) after report=%+v", r21)
	}
	if r22 := getm(b, 2, 2); r22.Left != 2 || r22.Right != 0 {
		t.Fatalf("N=6 (2,2)=%+v", r22)
	}
	if err := b.Report(1, 4, 3); err != nil {
		t.Fatal(err)
	}
	if r22 := getm(b, 2, 2); r22.Left != 2 || r22.Right != 3 || !r22.Ready {
		t.Fatalf("N=6 (2,2) after report=%+v", r22)
	}

	// N=8：首轮全部就绪，无轮空。
	b, _ = New(8)
	for i := 1; i <= 4; i++ {
		if m := getm(b, 1, i); !m.Ready || m.Kind != KindOpen {
			t.Fatalf("N=8 (1,%d)=%+v, expect ready open", i, m)
		}
	}

	// N=64：共 6 轮 63 场，首轮 32 场全部有人，无轮空。
	b, _ = New(64)
	if len(b.Bracket()) != 63 {
		t.Fatalf("N=64 match count=%d, want 63", len(b.Bracket()))
	}
	for i := 1; i <= 32; i++ {
		if m := getm(b, 1, i); !m.Ready || m.Kind != KindOpen {
			t.Fatalf("N=64 (1,%d)=%+v", i, m)
		}
	}
}
