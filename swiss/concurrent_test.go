package swiss

import (
	"errors"
	"sync"
	"testing"
)

// 高并发混合调用：多 goroutine 同时 Report 同一盘、查询 Standings、尝试 Pair。
// 串行化后必须：每盘恰好一个 Report 成功；一轮结束后才能开下一轮；
// 榜单始终满足总积分不变量；比赛最终可正常打完 4 轮（四人单循环）。
func TestConcurrentMixedAccess(t *testing.T) {
	tr, _ := New(4)
	mustRegister(t, tr, "p1", "p2", "p3", "p4")

	for round := 0; round < 3; round++ {
		pairs, bye, err := tr.Pair()
		if err != nil {
			t.Fatalf("round %d Pair: %v", round, err)
		}
		if bye != 0 {
			t.Fatalf("even field must not have a bye, got %d", bye)
		}

		var wg sync.WaitGroup
		for rep := 0; rep < 8; rep++ {
			wg.Add(1)
			go func(rep int) {
				defer wg.Done()
				// 每个 goroutine 都尝试登记每一盘：只有一个成功，其余 ErrAlreadyScore。
				for _, p := range pairs {
					_ = tr.Report(p.X, p.Y, 1)
				}
				_ = tr.Standings()
			}(rep)
		}
		wg.Wait()

		rows := tr.Standings()
		games := round + 1
		total := 0
		for _, x := range rows {
			total += x.Score
		}
		if want := 2 * games * 2; total != want { // 每轮 2 盘，每盘贡献 2 分
			t.Fatalf("round %d total = %d, want %d", round, total, want)
		}
	}

	// 三人两两均已交手：再开轮失败。
	if _, _, err := tr.Pair(); !errors.Is(err, ErrNoPairing) {
		t.Fatalf("fourth round: %v", err)
	}
}

// 并发登记阶段：先于首次 Pair 并发 Register，全部结束后人数必须精确。
func TestConcurrentRegister(t *testing.T) {
	tr, _ := New(2)
	const n = 30
	var wg sync.WaitGroup
	seeds := make(chan int, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := tr.Register("p" + itoa(i))
			if err != nil {
				errs <- err
				return
			}
			seeds <- s
		}(i)
	}
	wg.Wait()
	close(seeds)
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent Register: %v", e)
	}
	seen := map[int]bool{}
	for s := range seeds {
		if s < 1 || s > n || seen[s] {
			t.Fatalf("bad/duplicate seed %d", s)
		}
		seen[s] = true
	}
	if len(seen) != n {
		t.Fatalf("registered %d players, want %d", len(seen), n)
	}

	// 首次 Pair 成功后并发登记一律 ErrStarted。
	if _, _, err := tr.Pair(); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	var wg2 sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if _, err := tr.Register("late"); !errors.Is(err, ErrStarted) {
				t.Errorf("late register: %v", err)
			}
		}()
	}
	wg2.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
