package log

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// TestPrefixes 钉住不变量 3：任意一个前缀都验证一致（-1）。
func TestPrefixes(t *testing.T) {
	l := buildChain(t, 50)
	for n := 1; n <= len(l.Entries()); n++ {
		if FromSnapshot(l.entries[:n]).Verify() != -1 {
			t.Fatalf("prefix length %d failed verify", n)
		}
	}
}

// TestHeadReadsConstant 钉住复杂度：多档 m 下末次 Append 前驱读取数 ≤ 1，
// 不随 m 线性增长。reads 为非导出字段，仅同包测试可读，公开接口不可见。
func TestHeadReadsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		for i := 0; i < m; i++ {
			if _, err := l.Append(int64(i), "w", "op"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := l.Append(int64(m), "w", "op"); err != nil || l.reads > 1 {
			t.Fatalf("m=%d: reads=%d, want <= 1 and no error", m, l.reads)
		}
	}
}

// TestRandomAppendOrder 随机洗牌到达：Seq 连续无空洞、TS 非递减、链始终自洽。
func TestRandomAppendOrder(t *testing.T) {
	type rq struct {
		ts      int64
		who, op string
	}
	for _, size := range []int{1, 10, 100} {
		rng := rand.New(rand.NewSource(int64(size)))
		var rs []rq
		for _, v := range rng.Perm(size) {
			rs = append(rs, rq{int64(v), "w", "op"})
		}
		rs = append(rs, rq{-1, "w", "op"}, rq{0, "", "op"}, rq{0, "w", ""})
		rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		l, ok := New(), 0
		for _, r := range rs {
			if _, err := l.Append(r.ts, r.who, r.op); err == nil {
				ok++
			}
		}
		es := l.Entries()
		if len(es) != ok+1 || l.Verify() != -1 {
			t.Fatalf("size=%d: len=%d accepted=%d verify=%d", size, len(es), ok, l.Verify())
		}
		for i, e := range es {
			if e.Seq != int64(i) || (i > 0 && e.TS < es[i-1].TS) {
				t.Fatalf("size=%d: seq/ts invariant broken at %d", size, i)
			}
		}
	}
}

// TestConcurrentReaders N 个 goroutine 并发调用 Verify/Affected，结果逐一相同。
// 不使用 sleep 制造时序；配合 go test -race 检查数据竞争。
func TestConcurrentReaders(t *testing.T) {
	l := buildChain(t, 200)
	wantV, wantA := l.Verify(), l.Affected(7)
	const N = 32
	var wg sync.WaitGroup
	bad := make(chan struct{}, 1)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				if l.Verify() != wantV || !reflect.DeepEqual(l.Affected(7), wantA) {
					select {
					case bad <- struct{}{}:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case <-bad:
		t.Fatal("concurrent reader got divergent result")
	default:
	}
}

// TestSelfCheck 内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
