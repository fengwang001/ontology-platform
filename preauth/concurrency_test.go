package preauth

import (
	"fmt"
	"sync"
	"testing"
)

// 并发正确性：多 goroutine 对同一账户做授权/查询，结果必须等价于某串行序。
// 不变量：任何时刻可用额度不得为负；被接受的授权持有总额不得超过额度。
func TestConcurrentNoNegativeAvailable(t *testing.T) {
	s := NewSystem(Config{ValidityDays: 100, ToleranceBPS: 0})
	mustOK(t, s.CreateAccount("C", 1000, 0), "create")

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				now := int64(i + 1) // 单调不减，跨 goroutine 可能回退 => 允许报错
				err := s.Authorize(id, "C", 7, now)
				if err != nil && err != ErrInsufficient && err != ErrClockBackward {
					t.Errorf("unexpected auth err %v", err)
					return
				}
				if v, err := s.Available("C", now); err == nil && v < 0 {
					t.Errorf("negative available %d", v)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// 终态：所有被接受的授权均有效（now 未过期），持有合计 = 接受数*7，
	// 可用额度非负。
	v, err := s.Available("C", 201)
	mustOK(t, err, "final available")
	if v < 0 {
		t.Fatalf("final available negative %d", v)
	}
	acc, err := s.Account("C", 201)
	mustOK(t, err, "final account")
	if acc.OnHold+acc.Posted > acc.Credit {
		t.Fatalf("holds+posted %d exceed credit %d", acc.OnHold+acc.Posted, acc.Credit)
	}
}

// 同一操作序列重放必须得到完全相同的结果：把一串固定操作跑两遍比较。
func TestDeterministicReplay(t *testing.T) {
	type step struct {
		fn    func(sys *System) error
		check func(sys *System) (int64, error)
	}
	build := func() []step {
		return []step{
			{fn: func(x *System) error { return x.CreateAccount("C", 100, 0) }},
			{fn: func(x *System) error { return x.Authorize("A", "C", 60, 0) }},
			{fn: func(x *System) error { return x.Increment("A", 20, 2) }},
			{fn: func(x *System) error { return x.Capture("A", 95, false, 3) }}, // 60+20=80，上限88，95超容差
			{fn: func(x *System) error { return x.Capture("A", 88, false, 3) }},
			{check: func(x *System) (int64, error) { return x.Available("C", 3) }},
			{fn: func(x *System) error { return x.Refund("A", 8, 4) }},
			{check: func(x *System) (int64, error) { return x.Available("C", 4) }},
			{fn: func(x *System) error { return x.Void("A", 4) }}, // 已持有为0，终结
			{check: func(x *System) (int64, error) { return x.Available("C", 4) }},
		}
	}

	run := func() []string {
		sys := NewSystem(Config{ValidityDays: 5, ToleranceBPS: 1000})
		var out []string
		for _, st := range build() {
			if st.fn != nil {
				out = append(out, fmt.Sprint(st.fn(sys)))
			} else {
				v, err := st.check(sys)
				out = append(out, fmt.Sprintf("v=%d err=%v", v, err))
			}
		}
		return out
	}

	r1, r2 := run(), run()
	if len(r1) != len(r2) {
		t.Fatalf("length differs")
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("step %d: %s != %s\nr1=%v\nr2=%v", i, r1[i], r2[i], r1, r2)
		}
	}
	want := []string{
		"<nil>", "<nil>", "<nil>",
		"preauth: capture exceeds tolerance",
		"<nil>",
		"v=12 err=<nil>", // 100-入账88=12
		"<nil>",
		"v=20 err=<nil>",                // 入账80
		"preauth: authorization closed", // 非终捕未终结前 A 仍 active？
		"v=20 err=<nil>",
	}
	// A 捕获88后剩余持有为0但状态仍 active（非终捕、未过期），Void 应成功。
	want[8] = "<nil>"
	want[9] = "v=20 err=<nil>"
	for i := range want {
		if r1[i] != want[i] {
			t.Fatalf("step %d got %s want %s\n%v", i, r1[i], want[i], r1)
		}
	}
}
