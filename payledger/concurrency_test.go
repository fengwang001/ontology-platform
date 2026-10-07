package payledger

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// 并发调用：结果等价于某个串行顺序；任何时刻（不早于时钟的查询）可用额度非负。
// 需配合 go test -race 运行。
func TestConcurrentOps(t *testing.T) {
	l := NewLedger(Config{ExpiryDays: 3, ToleranceBps: 1000})
	mustOK(t, l.CreateAccount("c0", 100000, 0))
	mustOK(t, l.CreateAccount("c1", 100000, 0))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				now := int64(i / 10)
				acct := fmt.Sprintf("c%d", g%2)
				auth := fmt.Sprintf("g%d-a%d", g, i)
				switch r.Intn(5) {
				case 0:
					_ = l.Authorize(acct, auth, int64(1+r.Intn(100)), now)
				case 1:
					_ = l.Increment(auth, int64(1+r.Intn(50)), now)
				case 2:
					_ = l.Capture(auth, int64(1+r.Intn(80)), r.Intn(2) == 0, now)
				case 3:
					_ = l.Void(auth, now)
				case 4:
					_ = l.Refund(auth, int64(1+r.Intn(30)), now)
				}
				if avail, err := l.Available(acct, 1<<40); err == nil && avail < 0 {
					t.Errorf("并发下可用额度为负: %s = %d", acct, avail)
					return
				}
				if _, err := l.AuthSnapshot(auth, 1<<40); err != nil {
					if e := err.(*Error); e.Kind != ErrAuthNotFound {
						t.Errorf("意外的快照错误: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	cfg := Config{ExpiryDays: 5, ToleranceBps: 55}
	run := func() []string {
		l := NewLedger(cfg)
		r := rand.New(rand.NewSource(42))
		var out []string
		for _, o := range genOps(r, 2000) {
			err := applyOp(l, o)
			out = append(out, fmt.Sprintf("%s => %v", o, err))
		}
		for _, acct := range []string{"c0", "c1", "c2", "c3"} {
			for _, qn := range []int64{0, 7, 100, 1 << 30} {
				av, err := l.Available(acct, qn)
				out = append(out, fmt.Sprintf("avail(%s,%d)=%d,%v", acct, qn, av, err))
			}
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("重放长度不一致")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放结果不一致, 第 %d 项:\n  %s\n  %s", i, first[i], second[i])
		}
	}
}
