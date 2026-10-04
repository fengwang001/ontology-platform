package ingest

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestTableExample 复现题给主例，逐步打印输入/输出与判定依据。
func TestTableExample(t *testing.T) {
	r := NewRegistry()
	mustOK(t, r.Register("d", cfg4232()))
	for _, s := range []int64{1, 2, 3, 10} {
		mustOK(t, r.Ingest("d", s, 0))
	}
	out, err := r.Plan("d", 100, 5)
	mustOK(t, err)
	t.Logf("输入 Plan(100,5) 输出=%v 判定: 缺口[4,9],Lm=4 与 budget=5 切片", out)
	if fmt.Sprint(out) != "[{4 7} {8 8}]" {
		t.Fatalf("plan1=%v", out)
	}
	mustOK(t, r.Ingest("d", 5, 110))
	mustOK(t, r.Ingest("d", 6, 110))
	out, _ = r.Plan("d", 120, 10)
	t.Logf("输入 Ingest5,6@110;Plan(120) 输出=%v 判定: 4,7,8 在途未超时,仅9可请求", out)
	if fmt.Sprint(out) != "[{9 9}]" {
		t.Fatalf("plan2=%v", out)
	}
	out, _ = r.Plan("d", 130, 10)
	t.Logf("输入 Plan(130) 输出=%v 判定: 恰等超时,c=1<2 翻回,7/8 相邻同 c", out)
	if fmt.Sprint(out) != "[{4 4} {7 8}]" {
		t.Fatalf("plan3=%v", out)
	}
	out, _ = r.Plan("d", 160, 10)
	t.Logf("输入 Plan(160) 输出=%v 判定: 4,[7,9) 第2次超时判丢;9(c=1)翻回重发", out)
	if fmt.Sprint(out) != "[{9 9}]" {
		t.Fatalf("plan4=%v", out)
	}
	s, _ := r.Stats("d")
	t.Logf("Stats=%+v 判定: f=8,Lost=3,Missing=1(9 在途)", s)
	if s.F != 8 || s.Lost != 3 || s.Missing != 1 {
		t.Fatalf("stats160=%+v", s)
	}
	mustOK(t, r.Ingest("d", 4, 170))
	s, _ = r.Stats("d")
	t.Logf("输入 Ingest4@170 Stats=%+v 判定: 4 已 Lost,迟到记 Late 不恢复", s)
	if s.Late != 1 || s.Lost != 3 {
		t.Fatalf("late=%+v", s)
	}
	mustOK(t, r.Hello("d", 10, 12, 180))
	s, _ = r.Stats("d")
	t.Logf("输入 Hello(10,12,180) Stats=%+v 判定: 9 在途仍判丢,11,12 新增 Missing", s)
	if s.F != 10 || s.Hi != 12 || s.Lo != 10 || s.Missing != 2 {
		t.Fatalf("hello=%+v", s)
	}
	out, _ = r.Plan("d", 180, 10)
	t.Logf("输入 Plan(180) 输出=%v 判定: 仅剩缺口 11,12", out)
	if fmt.Sprint(out) != "[{11 12}]" {
		t.Fatalf("plan5=%v", out)
	}
}

func cfg4232() Config { return Config{Lm: 4, K: 2, Tq: 30, R: 2} }

// TestErrors 校验错误优先级与“拒绝不改状态”。
func TestErrors(t *testing.T) {
	r := NewRegistry()
	cfg := cfg4232()
	mustOK(t, r.Register("d", cfg))
	if !errors.Is(r.Register("d", cfg), ErrExists) {
		t.Fatal("重复注册应 ErrExists")
	}
	if !errors.Is(r.Register("", cfg), ErrInvalid) {
		t.Fatal("空设备名非法")
	}
	if !errors.Is(r.Register("x", Config{Lm: 0}), ErrInvalid) {
		t.Fatal("Lm 越界非法")
	}
	if !errors.Is(r.Register("x", Config{Lm: 1, K: 101, Tq: 1, R: 1}), ErrInvalid) {
		t.Fatal("K 越界非法")
	}
	if !errors.Is(r.Ingest("nope", 0, 0), ErrInvalid) {
		t.Fatal("ErrInvalid 优先于 ErrNoDevice")
	}
	if !errors.Is(r.Hello("nope", 0, 0, 0), ErrInvalid) {
		t.Fatal("Hello 非法参数优先")
	}
	if _, err := r.Plan("nope", 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("budget=0 非法优先")
	}
	if !errors.Is(r.Ingest("nope", 1, 0), ErrNoDevice) {
		t.Fatal("合法参数未知设备应 ErrNoDevice")
	}
	mustOK(t, r.Ingest("d", 1, 10))
	if !errors.Is(r.Ingest("d", 2, 9), ErrClockBack) {
		t.Fatal("时钟回退")
	}
	if _, err := r.Plan("d", 9, 1); !errors.Is(err, ErrClockBack) {
		t.Fatal("Plan 时钟回退")
	}
	if !errors.Is(r.Hello("d", 2, 5, 9), ErrClockBack) {
		t.Fatal("Hello 时钟回退")
	}
	mustOK(t, r.Hello("d", 2, 5, 11))
	if !errors.Is(r.Hello("d", 1, 5, 12), ErrRegress) {
		t.Fatal("lo 回退")
	}
	if !errors.Is(r.Hello("d", 2, 4, 12), ErrRegress) {
		t.Fatal("hi 回退")
	}
	// Hello 空缓冲 lo2=hi2+1 合法。
	mustOK(t, r.Hello("d", 6, 5, 13))
	before, _ := r.Stats("d")
	_ = r.Hello("d", 1, 5, 14)
	after, _ := r.Stats("d")
	if before != after {
		t.Fatalf("被拒绝操作改变状态: %+v -> %+v", before, after)
	}
}

// TestConservation 守恒式与 hi/f 单调。
func TestConservation(t *testing.T) {
	r := NewRegistry()
	mustOK(t, r.Register("d", Config{Lm: 3, K: 3, Tq: 10, R: 2}))
	rng := rand.New(rand.NewSource(7))
	var prevF, prevHi int64 = -1, 0
	for i := 0; i < 1000; i++ {
		now := int64(rng.Intn(300))
		s, _ := r.Stats("d")
		switch rng.Intn(3) {
		case 0:
			seq := s.Hi + int64(rng.Intn(8))
			if seq < 1 {
				seq = 1
			}
			_ = r.Ingest("d", seq, now)
		case 1:
			if s.Hi > 0 {
				_ = r.Ingest("d", int64(rng.Intn(int(s.Hi)))+1, now)
			}
		default:
			_, _ = r.Plan("d", now, int64(1+rng.Intn(12)))
		}
		st, _ := r.Stats("d")
		if st.Received+st.Lost+st.Missing != st.Hi {
			t.Fatalf("iter %d 守恒破坏 %+v", i, st)
		}
		if st.F < prevF || st.Hi < prevHi {
			t.Fatalf("iter %d 单调性破坏 f:%d<%d hi:%d<%d", i, st.F, prevF, st.Hi, prevHi)
		}
		prevF, prevHi = st.F, st.Hi
	}
}

// TestConcurrent 并发调用不崩，结束后守恒式成立。
func TestConcurrent(t *testing.T) {
	r := NewRegistry()
	mustOK(t, r.Register("d", Config{Lm: 4, K: 4, Tq: 5, R: 2}))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				now := int64(rng.Intn(500))
				switch rng.Intn(3) {
				case 0:
					_ = r.Ingest("d", int64(rng.Intn(300))+1, now)
				case 1:
					_, _ = r.Plan("d", now, int64(1+rng.Intn(20)))
				default:
					_, _ = r.Stats("d")
				}
			}
		}(int64(g))
	}
	wg.Wait()
	s, _ := r.Stats("d")
	if s.Received+s.Lost+s.Missing != s.Hi {
		t.Fatalf("并发后守恒破坏 %+v", s)
	}
}
