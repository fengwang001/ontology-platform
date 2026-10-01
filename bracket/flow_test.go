package bracket

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestFullReportToChampion(t *testing.T) {
	// N=8，种子1每场都胜 -> 冠军1；种子1与2只在决赛相遇。
	b, _ := New(8)
	ops := []struct{ r, i, w int }{
		{1, 1, 1}, // 1 vs 8
		{1, 2, 4}, // 4 vs 5
		{2, 1, 1}, // 1 vs 4
		{1, 3, 2}, // 2 vs 7
		{1, 4, 3}, // 3 vs 6
		{2, 2, 2}, // 2 vs 3
		{3, 1, 1}, // 决赛 1 vs 2
	}
	for _, op := range ops {
		m := getm(b, op.r, op.i)
		if !m.Ready || m.Kind != KindOpen {
			t.Fatalf("before report (%d,%d)=%+v, expect ready open", op.r, op.i, m)
		}
		if err := b.Report(op.r, op.i, op.w); err != nil {
			t.Fatalf("Report(%d,%d,%d): %v", op.r, op.i, op.w, err)
		}
	}
	if c := b.Champion(); c != 1 {
		t.Fatalf("champion=%d, want 1", c)
	}
	final := getm(b, 3, 1)
	if final.Left != 1 || final.Right != 2 || final.Winner != 1 || final.Kind != KindManual {
		t.Fatalf("final=%+v", final)
	}
	// 冠军已决出：冠军本人退赛也被拒（已淘汰者会先命中 ErrAlreadyEliminated）。
	if err := b.Withdraw(1); !errors.Is(err, ErrChampionDecided) {
		t.Fatalf("withdraw after champion err=%v, want ErrChampionDecided", err)
	}
	before := fmt.Sprint(b.Bracket())
	if err := b.Report(2, 2, 3); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("report after champion err=%v", err)
	}
	if after := fmt.Sprint(b.Bracket()); after != before {
		t.Fatal("rejected op changed state")
	}
}

func TestReportRejectOrder(t *testing.T) {
	b, _ := New(6)
	if err := b.Report(9, 1, 1); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("err=%v want ErrMatchNotFound", err)
	}
	if err := b.Report(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("err=%v want ErrByeMatch", err)
	}
	// 首轮尚未登记前，(2,1) 右位空缺，未就绪。
	if err := b.Report(2, 1, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err=%v want ErrNotReady", err)
	}
	if err := b.Report(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(1, 2, 4); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("err=%v want ErrAlreadyDecided", err)
	}
	if err := b.Report(1, 4, 3); err != nil {
		t.Fatal(err)
	}
	// 此时 (2,1)=1 vs 4 就绪：99 不是参赛者。
	if err := b.Report(2, 1, 99); !errors.Is(err, ErrNotAParticipant) {
		t.Fatalf("err=%v want ErrNotAParticipant", err)
	}
}

func TestCorrectFlow(t *testing.T) {
	b, _ := New(8)
	if err := b.Correct(9, 1, 1); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("err=%v", err)
	}
	bb, _ := New(3)
	if err := bb.Correct(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("err=%v want ErrByeMatch", err)
	}
	if err := b.Correct(1, 1, 1); !errors.Is(err, ErrNoResult) {
		t.Fatalf("err=%v want ErrNoResult", err)
	}
	if err := b.Report(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	// 下一轮尚无结果：更正为 8，新胜者进入同一左位，(2,1)=8 vs 4。
	if err := b.Correct(1, 1, 8); err != nil {
		t.Fatalf("correct to 8: %v", err)
	}
	if m := getm(b, 1, 1); m.Winner != 8 || m.Kind != KindManual {
		t.Fatalf("(1,1)=%+v", m)
	}
	if m := getm(b, 2, 1); m.Left != 8 || m.Right != 4 || !m.Ready {
		t.Fatalf("(2,1)=%+v", m)
	}
	if err := b.Correct(1, 1, 4); !errors.Is(err, ErrNotAParticipant) {
		t.Fatalf("err=%v want ErrNotAParticipant", err)
	}
	if err := b.Correct(1, 1, 8); !errors.Is(err, ErrAlreadyWinner) {
		t.Fatalf("err=%v want ErrAlreadyWinner", err)
	}
	if err := b.Report(2, 1, 8); err != nil {
		t.Fatal(err)
	}
	snap := fmt.Sprint(b.Bracket())
	if err := b.Correct(1, 1, 1); !errors.Is(err, ErrNextRoundDecided) {
		t.Fatalf("err=%v want ErrNextRoundDecided", err)
	}
	if after := fmt.Sprint(b.Bracket()); after != snap {
		t.Fatal("rejected correct changed state")
	}
	if err := b.Withdraw(1); !errors.Is(err, ErrAlreadyEliminated) {
		t.Fatalf("err=%v want ErrAlreadyEliminated", err)
	}
}

func TestCorrectFinal(t *testing.T) {
	b, _ := New(3)
	if err := b.Report(1, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(2, 1, 1); err != nil {
		t.Fatal(err)
	}
	if c := b.Champion(); c != 1 {
		t.Fatalf("champion=%d", c)
	}
	if err := b.Correct(2, 1, 2); err != nil {
		t.Fatalf("correct final: %v", err)
	}
	if c := b.Champion(); c != 2 {
		t.Fatalf("champion after correct=%d, want 2", c)
	}
	if m := getm(b, 2, 1); m.Winner != 2 || m.Kind != KindManual {
		t.Fatalf("final=%+v", m)
	}
	// 旧冠军1此时已是决赛败者（已淘汰，先命中 ErrAlreadyEliminated）。
	if err := b.Withdraw(1); !errors.Is(err, ErrAlreadyEliminated) {
		t.Fatalf("err=%v want ErrAlreadyEliminated", err)
	}
	if err := b.Withdraw(2); !errors.Is(err, ErrChampionDecided) {
		t.Fatalf("err=%v want ErrChampionDecided", err)
	}
}

func TestWithdrawCascade(t *testing.T) {
	// N=3：种子1轮空等待决赛；在对手未定时退赛，2vs3 一登记，对手立即自动晋级夺冠。
	b, _ := New(3)
	if err := b.Withdraw(1); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(1, 2, 2); err != nil {
		t.Fatal(err)
	}
	m12 := getm(b, 1, 2)
	if m12.Winner != 2 || m12.Kind != KindManual {
		t.Fatalf("(1,2)=%+v, want manual 2", m12)
	}
	final := getm(b, 2, 1)
	if final.Left != 1 || final.Right != 2 || final.Winner != 2 || final.Kind != KindTechnical {
		t.Fatalf("final=%+v, want technical win for 2", final)
	}
	if c := b.Champion(); c != 2 {
		t.Fatalf("champion=%d, want 2 (cascade)", c)
	}
	if err := b.Correct(2, 1, 1); !errors.Is(err, ErrTechnicalResult) {
		t.Fatalf("err=%v want ErrTechnicalResult", err)
	}
}

func TestWithdrawSingleSideAuto(t *testing.T) {
	b, _ := New(8)
	if err := b.Withdraw(8); err != nil {
		t.Fatal(err)
	}
	m := getm(b, 1, 1)
	if m.Winner != 1 || m.Kind != KindTechnical {
		t.Fatalf("(1,1)=%+v, want technical 1", m)
	}
	if err := b.Correct(1, 1, 8); !errors.Is(err, ErrTechnicalResult) {
		t.Fatalf("err=%v want ErrTechnicalResult", err)
	}
	if err := b.Withdraw(8); !errors.Is(err, ErrAlreadyWithdrawn) {
		t.Fatalf("err=%v want ErrAlreadyWithdrawn", err)
	}
	if err := b.Withdraw(1); err != nil {
		t.Fatalf("withdraw active winner: %v", err)
	}
}

func TestBothWithdrawnLowerSeed(t *testing.T) {
	// 公共 API 下每次 Withdraw 成功即级联到不动点，无法经外部操作让一场比赛在
	// 就绪的同一瞬态有两名退赛者（先退赛方会立即被判负）。这里以包内白盒方式
	// 直接构造该瞬态，验证 cascade 的“双方退赛种子小者晋级”分支。
	b, _ := New(4)
	m := b.matches[[2]int{2, 1}]
	m.left = 4
	m.right = 1
	b.withdrawn[4] = true
	b.withdrawn[1] = true
	b.cascade()
	if m.winner != 1 || m.kind != KindTechnical {
		t.Fatalf("final=%+v, want technical lower-seed 1", m)
	}
	if c := b.Champion(); c != 1 {
		t.Fatalf("champion=%d, want 1", c)
	}
}

func TestWithdrawRejectOrder(t *testing.T) {
	b, _ := New(8)
	if err := b.Withdraw(0); !errors.Is(err, ErrSeedOutOfRange) {
		t.Fatalf("err=%v want ErrSeedOutOfRange", err)
	}
	if err := b.Withdraw(99); !errors.Is(err, ErrSeedOutOfRange) {
		t.Fatalf("err=%v want ErrSeedOutOfRange", err)
	}
	if err := b.Report(1, 1, 8); err != nil {
		t.Fatal(err)
	}
	if err := b.Withdraw(1); !errors.Is(err, ErrAlreadyEliminated) {
		t.Fatalf("err=%v want ErrAlreadyEliminated", err)
	}
	if err := b.Withdraw(8); err != nil {
		t.Fatal(err)
	}
	if err := b.Withdraw(8); !errors.Is(err, ErrAlreadyWithdrawn) {
		t.Fatalf("err=%v want ErrAlreadyWithdrawn", err)
	}
	b2, _ := New(8)
	for _, op := range [][3]int{
		{1, 1, 1}, {1, 2, 4}, {2, 1, 1},
		{1, 3, 2}, {1, 4, 3}, {2, 2, 2}, {3, 1, 1},
	} {
		if err := b2.Report(op[0], op[1], op[2]); err != nil {
			t.Fatal(err)
		}
	}
	snap := fmt.Sprint(b2.Bracket())
	if err := b2.Withdraw(1); !errors.Is(err, ErrChampionDecided) {
		t.Fatalf("err=%v want ErrChampionDecided", err)
	}
	if after := fmt.Sprint(b2.Bracket()); after != snap {
		t.Fatal("rejected withdraw changed state")
	}
}

func TestN6FullFlow(t *testing.T) {
	b, _ := New(6)
	if err := b.Report(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(1, 4, 3); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(2, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Report(2, 2, 2); err != nil {
		t.Fatal(err)
	}
	final := getm(b, 3, 1)
	if final.Left != 1 || final.Right != 2 {
		t.Fatalf("final=%+v, want 1 vs 2", final)
	}
	if err := b.Report(3, 1, 2); err != nil {
		t.Fatal(err)
	}
	if c := b.Champion(); c != 2 {
		t.Fatalf("champion=%d", c)
	}
}

func TestConcurrentCalls(t *testing.T) {
	b, _ := New(8)
	var wg sync.WaitGroup
	// 并发地反复尝试所有操作；等价于某个串行顺序，结束后全树不变量必须成立。
	try := func(f func() error) {
		defer wg.Done()
		for k := 0; k < 50; k++ {
			_ = f()
		}
	}
	wg.Add(6)
	go try(func() error { return b.Report(1, 1, 1) })
	go try(func() error { return b.Report(1, 2, 4) })
	go try(func() error { return b.Correct(1, 1, 8) })
	go try(func() error { return b.Withdraw(7) })
	go try(func() error { return b.Withdraw(6) })
	go try(func() error { _ = b.Champion(); _ = b.Bracket(); return nil })
	wg.Wait()
	for _, m := range b.Bracket() {
		if m.Winner != 0 && m.Winner != m.Left && m.Winner != m.Right {
			t.Fatalf("winner %d not a participant: %+v", m.Winner, m)
		}
		if m.Kind == KindBye && m.Ready {
			t.Fatalf("bye match reported ready: %+v", m)
		}
	}
}

func TestConcurrentLinearizability(t *testing.T) {
	// 多 goroutine 高强度争用同一棵树：互斥使结果等价于某一串行顺序，
	// 并发结束后全树必须保持“每场一个合法胜者 / 冠军唯一”不变量。
	for iter := 0; iter < 20; iter++ {
		b, _ := New(16)
		var wg sync.WaitGroup
		worker := func(fn func(int) error) {
			defer wg.Done()
			for k := 1; k <= 16; k++ {
				_ = fn(k)
			}
		}
		wg.Add(8)
		go worker(func(k int) error { return b.Report(1, (k%8)+1, (k%16)+1) })
		go worker(func(k int) error { return b.Report(1, (k%8)+1, ((k+3)%16)+1) })
		go worker(func(k int) error { return b.Correct(1, (k%8)+1, (k%16)+1) })
		go worker(func(k int) error { return b.Withdraw(k) })
		go worker(func(k int) error { return b.Withdraw(k + 0) })
		go worker(func(k int) error {
			_ = b.Bracket()
			return nil
		})
		go worker(func(k int) error {
			_ = b.Champion()
			return nil
		})
		go worker(func(k int) error { return b.Report(4, 1, k) })
		wg.Wait()

		winCount := map[int]int{}
		finalCount := 0
		for _, m := range b.Bracket() {
			if m.Winner != 0 {
				if m.Winner != m.Left && m.Winner != m.Right {
					t.Fatalf("iter=%d illegal winner %+v", iter, m)
				}
				winCount[m.Winner]++
			}
			if m.Round == b.rounds && m.Index == 1 && m.Kind != KindOpen {
				finalCount++
			}
		}
		if finalCount > 1 {
			t.Fatalf("iter=%d %d finals", iter, finalCount)
		}
		if finalCount == 1 && b.Champion() == 0 {
			t.Fatalf("iter=%d final decided but no champion", iter)
		}
	}
}
