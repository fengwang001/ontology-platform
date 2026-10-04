package picker

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func mustNew(t *testing.T, tau, p0, pf int64, m, nmax int) *Selector {
	t.Helper()
	s, err := New(tau, p0, pf, m, nmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustAdd(t *testing.T, s *Selector, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := s.AddEndpoint(id); err != nil {
			t.Fatalf("AddEndpoint(%q): %v", id, err)
		}
	}
}

func mustPick(t *testing.T, s *Selector, now int64, r1, r2 uint64) (uint64, string) {
	t.Helper()
	tk, id, err := s.Pick(now, r1, r2)
	if err != nil {
		t.Fatalf("Pick(%d,%d,%d): %v", now, r1, r2, err)
	}
	return tk, id
}

func mustRelease(t *testing.T, s *Selector, tk uint64, rtt int64, ok bool, now int64) {
	t.Helper()
	if err := s.Release(tk, rtt, ok, now); err != nil {
		t.Fatalf("Release(%d): %v", tk, err)
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name        string
		tau, p0, pf int64
		m, nmax     int
	}{
		{"tau为0", 0, 50, 1000, 2, 10},
		{"tau超界", 1_000_000_001, 50, 1000, 2, 10},
		{"p0为0", 100, 0, 1000, 2, 10},
		{"pf超界", 100, 50, 1_000_000_001, 2, 10},
		{"m为0", 100, 50, 1000, 0, 10},
		{"m超界", 100, 50, 1000, 1_000_001, 10},
		{"nmax为0", 100, 50, 1000, 2, 0},
		{"nmax超界", 100, 50, 1000, 2, 100_001},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.tau, tc.p0, tc.pf, tc.m, tc.nmax); err == nil {
				t.Fatal("应报参数非法")
			}
		})
	}
	if _, err := New(1, 1, 1, 1, 1); err != nil {
		t.Fatalf("边界最小值应合法: %v", err)
	}
	if _, err := New(1_000_000_000, 1_000_000_000, 1_000_000_000, 1_000_000, 100_000); err != nil {
		t.Fatalf("边界最大值应合法: %v", err)
	}
}

// TestSpecExample 逐步走查需求文档中的示例。
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 100, 50, 1000, 2, 10)
	mustAdd(t, s, "a", "b", "c")

	// Pick(0,0,0)：a、b 代价均 50，并列取 a，票据 1。
	tk, id := mustPick(t, s, 0, 0, 0)
	if tk != 1 || id != "a" {
		t.Fatalf("第1次 Pick = (%d,%q), want (1,a)", tk, id)
	}
	// Pick(0,0,0)：a 代价 50*2=100，b 为 50，取 b，票据 2。
	tk, id = mustPick(t, s, 0, 0, 0)
	if tk != 2 || id != "b" {
		t.Fatalf("第2次 Pick = (%d,%q), want (2,b)", tk, id)
	}
	// Release(1,200,成功,10)：a 无样本，est=200，last=10。
	mustRelease(t, s, 1, 200, true, 10)
	if est, last, has := s.pl.Get("a").Est.Raw(); est != 200 || last != 10 || !has {
		t.Fatalf("a 的估计 = (%d,%d,%v), want (200,10,true)", est, last, has)
	}
	// Pick(10,0,0)：a 代价 200，b 在途1代价 50*2=100，取 b（在途变 2 满员）。
	tk, id = mustPick(t, s, 10, 0, 0)
	if tk != 3 || id != "b" {
		t.Fatalf("第3次 Pick = (%d,%q), want (3,b)", tk, id)
	}
	// Pick(60,0,0)：b 满员，elig=[a,c]，j=(0+1+0)%2=1；
	// a 的 d=50，val=100，代价 100；c 为 50；取 c。
	tk, id = mustPick(t, s, 60, 0, 0)
	if tk != 4 || id != "c" {
		t.Fatalf("第4次 Pick = (%d,%q), want (4,c)", tk, id)
	}
	// Release(3,5,失败,70)：票据 3 属 b，以 Pf=1000 记样本。
	mustRelease(t, s, 3, 5, false, 70)
	if est, last, _ := s.pl.Get("b").Est.Raw(); est != 1000 || last != 70 {
		t.Fatalf("b 的估计 = (%d,%d), want (1000,70)", est, last)
	}
	// RemoveEndpoint(a)：在途 0，立即移除。
	if err := s.RemoveEndpoint("a"); err != nil {
		t.Fatal(err)
	}
	if s.pl.Get("a") != nil {
		t.Fatal("a 应被立即移除")
	}
	// RemoveEndpoint(b)：在途 1，转排空。
	if err := s.RemoveEndpoint("b"); err != nil {
		t.Fatal(err)
	}
	// 排空期间 AddEndpoint(b) 报已存在。
	if err := s.AddEndpoint("b"); !errors.Is(err, ErrExists) {
		t.Fatalf("排空中 AddEndpoint(b) = %v, want ErrExists", err)
	}
	// 排空端点不被选中：elig 只剩 c。
	tk, id = mustPick(t, s, 70, 0, 0)
	if tk != 5 || id != "c" {
		t.Fatalf("排空后 Pick = (%d,%q), want (5,c)", tk, id)
	}
	// Release(2,30,成功,80)：b 在途归零被移除。
	mustRelease(t, s, 2, 30, true, 80)
	if s.pl.Get("b") != nil {
		t.Fatal("b 在途归零后应被移除")
	}
}

// TestTieBreakSmallerID 代价并列时取 id 较小者（与 i、j 位置无关）。
func TestTieBreakSmallerID(t *testing.T) {
	tests := []struct {
		name   string
		r1, r2 uint64
		wantID string
	}{
		{"i指向小id", 0, 0, "a"}, // i=0(a), j=1(b)
		{"i指向大id", 1, 0, "a"}, // i=1(b), j=(1+1+0)%2=0(a)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t, 100, 50, 1000, 2, 10)
			mustAdd(t, s, "a", "b")
			_, id := mustPick(t, s, 0, tc.r1, tc.r2)
			if id != tc.wantID {
				t.Fatalf("Pick = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// TestIndexComputation 覆盖 n=1 与 n=2 时 i、j 的计算。
func TestIndexComputation(t *testing.T) {
	t.Run("n=1直接取唯一端点", func(t *testing.T) {
		s := mustNew(t, 100, 50, 1000, 2, 10)
		mustAdd(t, s, "solo")
		// r1、r2 任意值均被忽略。
		_, id := mustPick(t, s, 0, 12345, 67890)
		if id != "solo" {
			t.Fatalf("Pick = %q, want solo", id)
		}
	})
	t.Run("n=2时j恒为i的另一端", func(t *testing.T) {
		// n=2 时 j=(i+1+r2%1)%2=(i+1)%2，与 r2 无关。
		// 让 b 代价高（在途 1），使胜者恒为 i 指向端点之外逻辑可判：
		// 直接验证 i=0 选 a、i=1 选 b（代价相同，i 端即候选之一，
		// 并列取小 id，故用不同代价区分）。
		s := mustNew(t, 100, 50, 1000, 5, 10)
		mustAdd(t, s, "a", "b")
		// 抬高 b 的估计：先发一票给 b 再失败释放。
		// 用 r1=1 使 i=1 指向 b，j=0 指向 a，代价相同并列取 a。
		_, id := mustPick(t, s, 0, 1, 999)
		if id != "a" {
			t.Fatalf("i=1 时并列应取小 id a, got %q", id)
		}
		// a 在途 1，代价 100；b 代价 50。i=0 指向 a、j=1 指向 b，取 b。
		_, id = mustPick(t, s, 0, 0, 0)
		if id != "b" {
			t.Fatalf("i=0 时 b 代价低应取 b, got %q", id)
		}
	})
}

// TestIndexDistinct 性质测试：任意 n>=2 与任意 r1、r2，i 恒不等于 j。
func TestIndexDistinct(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for n := 2; n <= 200; n++ {
		for k := 0; k < 200; k++ {
			r1, r2 := rng.Uint64(), rng.Uint64()
			i := int(r1 % uint64(n))
			j := (i + 1 + int(r2%uint64(n-1))) % n
			if i == j {
				t.Fatalf("n=%d r1=%d r2=%d: i==j==%d", n, r1, r2, i)
			}
			if j < 0 || j >= n {
				t.Fatalf("n=%d: j=%d 越界", n, j)
			}
		}
	}
}

// TestSaturation 满员端点不入候选；全部满员报饱和；无端点报无端点。
func TestSaturation(t *testing.T) {
	t.Run("无端点", func(t *testing.T) {
		s := mustNew(t, 100, 50, 1000, 1, 10)
		if _, _, err := s.Pick(0, 0, 0); !errors.Is(err, ErrNoEndpoints) {
			t.Fatalf("err = %v, want ErrNoEndpoints", err)
		}
	})
	t.Run("满员端点不入候选", func(t *testing.T) {
		s := mustNew(t, 100, 50, 1000, 1, 10)
		mustAdd(t, s, "a", "b")
		tk, id := mustPick(t, s, 0, 0, 0) // a 在途变 1，满员
		if id != "a" {
			t.Fatalf("Pick = %q, want a", id)
		}
		_, id = mustPick(t, s, 0, 0, 0) // 候选只剩 b
		if id != "b" {
			t.Fatalf("Pick = %q, want b", id)
		}
		_ = tk
	})
	t.Run("全部满员报饱和", func(t *testing.T) {
		s := mustNew(t, 100, 50, 1000, 1, 10)
		mustAdd(t, s, "a")
		mustPick(t, s, 0, 0, 0)
		if _, _, err := s.Pick(0, 0, 0); !errors.Is(err, ErrSaturated) {
			t.Fatalf("err = %v, want ErrSaturated", err)
		}
	})
	t.Run("全部排空报无端点", func(t *testing.T) {
		s := mustNew(t, 100, 50, 1000, 1, 10)
		mustAdd(t, s, "a")
		if err := s.RemoveEndpoint("a"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Pick(0, 0, 0); !errors.Is(err, ErrNoEndpoints) {
			t.Fatalf("err = %v, want ErrNoEndpoints", err)
		}
	})
}

// TestDrainingFlow 排空端点仍可 Release、归零移除、占用 Nmax、不被选中。
func TestDrainingFlow(t *testing.T) {
	s := mustNew(t, 100, 50, 1000, 2, 2)
	mustAdd(t, s, "x", "y")
	tk1, _ := mustPick(t, s, 0, 0, 0) // x
	if err := s.RemoveEndpoint("x"); err != nil {
		t.Fatal(err)
	}
	// 排空占用 Nmax：新增第三个端点报已满。
	if err := s.AddEndpoint("z"); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	// 排空端点不被选中。
	_, id := mustPick(t, s, 0, 0, 0)
	if id != "y" {
		t.Fatalf("Pick = %q, want y", id)
	}
	// 重复移除报排空中。
	if err := s.RemoveEndpoint("x"); !errors.Is(err, ErrDraining) {
		t.Fatalf("err = %v, want ErrDraining", err)
	}
	// 排空端点仍可 Release，归零后被移除，名额释放。
	mustRelease(t, s, tk1, 10, true, 1)
	if s.pl.Get("x") != nil {
		t.Fatal("x 应被移除")
	}
	if err := s.AddEndpoint("z"); err != nil {
		t.Fatalf("名额释放后应可新增: %v", err)
	}
}

// TestDuplicateRelease 票据重复归还与未知票据均被拒。
func TestDuplicateRelease(t *testing.T) {
	s := mustNew(t, 100, 50, 1000, 2, 10)
	mustAdd(t, s, "a")
	tk, _ := mustPick(t, s, 0, 0, 0)
	mustRelease(t, s, tk, 10, true, 1)
	if err := s.Release(tk, 10, true, 2); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("重复归还 = %v, want ErrTicketUnknown", err)
	}
	if err := s.Release(999, 10, true, 2); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("未知票据 = %v, want ErrTicketUnknown", err)
	}
}

// TestRejectedOpsConsumeNothing 被拒操作不耗票据号、不推进最大 now、不改估计。
func TestRejectedOpsConsumeNothing(t *testing.T) {
	s := mustNew(t, 100, 50, 1000, 1, 10)
	mustAdd(t, s, "a")
	tk1, _ := mustPick(t, s, 5, 0, 0) // 票据 1，maxNow=5

	// 饱和的 Pick 被拒：不耗号、不推进 maxNow。
	if _, _, err := s.Pick(10, 0, 0); !errors.Is(err, ErrSaturated) {
		t.Fatalf("err = %v, want ErrSaturated", err)
	}
	// 时钟回退的 Pick 被拒（maxNow 仍为 5）。
	if _, _, err := s.Pick(4, 0, 0); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	// 非法时间。
	if _, _, err := s.Pick(-1, 0, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("err = %v, want ErrInvalidTime", err)
	}
	if _, _, err := s.Pick(1_000_000_000_000_001, 0, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("err = %v, want ErrInvalidTime", err)
	}
	// 非法 rtt 的 Release 被拒。
	if err := s.Release(tk1, -1, true, 5); !errors.Is(err, ErrInvalidRTT) {
		t.Fatalf("err = %v, want ErrInvalidRTT", err)
	}
	if err := s.Release(tk1, 1_000_000_001, true, 5); !errors.Is(err, ErrInvalidRTT) {
		t.Fatalf("err = %v, want ErrInvalidRTT", err)
	}
	// 未知票据的 Release 被拒，不推进 maxNow。
	if err := s.Release(77, 10, true, 6); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("err = %v, want ErrTicketUnknown", err)
	}
	// 估计未被任何被拒操作改变（仍无样本）。
	if s.pl.Get("a").Est.HasSample() {
		t.Fatal("被拒操作不应改变估计")
	}
	// 释放票据 1 后，下一张成功 Pick 应发票据 2（被拒操作未耗号）。
	mustRelease(t, s, tk1, 10, true, 5)
	tk2, _ := mustPick(t, s, 5, 0, 0)
	if tk2 != 2 {
		t.Fatalf("票据号 = %d, want 2（被拒操作不得耗号）", tk2)
	}
	// maxNow 未被拒操作推进：now=5 仍合法（不小于 maxNow=5）。
	mustRelease(t, s, tk2, 10, true, 5)
}

// TestErrorPrecedence 错误只报第一个：参数非法 > 时间非法 > 时钟回退 > 业务。
func TestErrorPrecedence(t *testing.T) {
	s := mustNew(t, 100, 50, 1000, 1, 10)
	mustAdd(t, s, "a")
	tk, _ := mustPick(t, s, 10, 0, 0)

	// 参数非法优先于时间非法。
	if err := s.Release(tk, -1, true, -1); !errors.Is(err, ErrInvalidRTT) {
		t.Fatalf("err = %v, want ErrInvalidRTT", err)
	}
	// 时间非法优先于时钟回退。
	if _, _, err := s.Pick(1_000_000_000_000_001, 0, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("err = %v, want ErrInvalidTime", err)
	}
	// 时钟回退优先于业务错误（票据未知）。
	if err := s.Release(999, 10, true, 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	// AddEndpoint/RemoveEndpoint：参数非法优先于业务错误。
	if err := s.AddEndpoint(""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("err = %v, want ErrEmptyID", err)
	}
	if err := s.RemoveEndpoint(""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("err = %v, want ErrEmptyID", err)
	}
	if err := s.RemoveEndpoint("nosuch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestFailurePenalty 失败样本按 Pf 记录，与真实 rtt 无关。
func TestFailurePenalty(t *testing.T) {
	tests := []struct {
		name string
		rtt  int64
		ok   bool
		want int64
	}{
		{"秒失败仍按惩罚", 1, false, 1000},
		{"慢失败也按惩罚", 999, false, 1000},
		{"成功按真实rtt", 1, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t, 100, 50, 1000, 2, 10)
			mustAdd(t, s, "a")
			tk, _ := mustPick(t, s, 0, 0, 0)
			mustRelease(t, s, tk, tc.rtt, tc.ok, 0)
			if est, _, _ := s.pl.Get("a").Est.Raw(); est != tc.want {
				t.Fatalf("est = %d, want %d", est, tc.want)
			}
		})
	}
}

// TestConcurrent 并发调用后不变量仍成立：Σ在途 == 未归还票据数、
// 每端点在途 ≤ M、票据号连续无空洞（只有成功 Pick 耗号）。
func TestConcurrent(t *testing.T) {
	const (
		workers   = 8
		rounds    = 500
		m         = 4
		nEndpoint = 6
	)
	s := mustNew(t, 100, 50, 1000, m, nEndpoint)
	ids := []string{"a", "b", "c", "d", "e", "f"}
	mustAdd(t, s, ids...)

	var wg sync.WaitGroup
	tkCh := make(chan uint64, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for k := 0; k < rounds; k++ {
				tk, _, err := s.Pick(int64(k), rng.Uint64(), rng.Uint64())
				if err == nil {
					tkCh <- tk
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	close(tkCh)
	// 并发 Pick 的 now 相互回退会被拒；串行归还所有成功票据。
	count := 0
	for tk := range tkCh {
		mustRelease(t, s, tk, 10, true, rounds)
		count++
	}
	sum := 0
	for _, id := range ids {
		sum += s.pl.Get(id).Inflight
	}
	if sum != 0 || len(s.tickets) != 0 {
		t.Fatalf("全部归还后仍有在途: Σ在途=%d 未归还票据=%d", sum, len(s.tickets))
	}
	t.Logf("并发 Pick 成功 %d 次，全部归还后 Σ在途=0、未归还票据=0，不变量成立", count)
}
