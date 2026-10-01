package ontology

import (
	"fmt"
	"math/bits"
	"sync"
	"testing"
)

// bound 为题目规定的比较上界：8*(floor(log2(n))+2)。
func cmpBound(n int) int64 {
	return int64(8 * (bits.Len(uint(n)) - 1 + 2))
}

// assertNonLinear 证明单次操作的 H 比较次数不随条目数线性增长。
//
// 判定依据（堆的标准复杂度）：
//   - 新键 Put 恰驱逐一个：1 次 heap.Pop（≤2⌊log2 n⌋ 次比较）+ 1 次 heap.Push
//     （≤⌊log2(n+1)⌋ 次比较），合计 < 3⌈log2(n+1)⌉，远低于 8(⌊log2 n⌋+2)。
//   - 覆盖写 Put：heap.Remove（≤2⌊log2 n⌋）+ heap.Push（≤⌊log2 n⌋），< 3⌈log2 n⌉。
//   - Get 命中：1 次 heap.Fix（上浮或下沉，≤2⌊log2 n⌋），< 2⌈log2 n⌉。
//
// 计数器只在堆的 Less 中、真正比较两个 H 时自增，因此读到的数字即本次操作的
// H 比较次数。
func TestComparisonComplexity(t *testing.T) {
	for _, n := range []int{1000, 50000} {
		n := n
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			bound := cmpBound(n)
			t.Logf("条目数=%d floor(log2 n)=%d 比较上界 8*(floor(log2 n)+2)=%d",
				n, bits.Len(uint(n))-1, bound)

			// 1) 新键 Put 恰驱逐一个：容量 n。先填满 n 条并再插一个高 cost 对象，
			//    使其中一次驱逐把 L 抬到 >0（此后堆内 H 均 ≥ L>0）；然后再插入
			//    一个足够大 cost 的新键，保证它不是最小者、恰好驱逐现有一个条目。
			c := mustCache(t, int64(n))
			for i := 0; i < n; i++ {
				if _, err := c.Put(fmt.Sprintf("k%d", i), 1, int64(1+(i*7)%100)); err != nil {
					t.Fatal(err)
				}
			}
			if c.Used() != int64(n) {
				t.Fatalf("setup used=%d want %d", c.Used(), n)
			}
			if L := c.inflation(); L.Sign() != 0 {
				t.Fatalf("setup L should still be 0, got %s", L.RatString())
			}
			// 高 cost 新键：必然驱逐当前 H 最小者，且其自身 H 很大，L 被抬升。
			if ev, err := c.Put("priming", 1, 1_000_000); err != nil || len(ev) != 1 {
				t.Fatalf("priming Put: ev=%v err=%v", ev, err)
			}
			if L := c.inflation(); L.Sign() == 0 {
				t.Fatalf("priming should advance L above 0")
			}
			if c.Used() != int64(n) {
				t.Fatalf("post-priming used=%d want %d", c.Used(), n)
			}
			// 再来一个高 cost 新键：L>0，新键 H=L+很大，必非最小者，恰好驱逐一个。
			ev, err := c.Put("new-evict", 1, 2_000_000)
			if err != nil || len(ev) != 1 {
				t.Fatalf("new-key Put: ev=%v err=%v, want exactly one eviction", ev, err)
			}
			got := c.lastComparisons()
			t.Logf("新键 Put 恰驱逐一个: H 比较 %d 次（上界 %d）", got, bound)
			if got > bound {
				t.Fatalf("new-key Put comparisons=%d exceeds bound=%d (n=%d)", got, bound, n)
			}

			// 2) 覆盖写 Put：容量 n、当前恰好 n 条（上一步插入补满到 n）。
			// k57 的 cost=1+(57*7)%100=100，H 很高，必未被此前的驱逐淘汰。
			if _, ok, _ := c.Peek("k57"); !ok {
				t.Fatalf("setup invariant: k57 must still be resident")
			}
			ev, err = c.Put("k57", 1, 55)
			if err != nil || len(ev) != 0 {
				t.Fatalf("overwrite Put: ev=%v err=%v, want no eviction", ev, err)
			}
			got = c.lastComparisons()
			t.Logf("覆盖写 Put: H 比较 %d 次（上界 %d）", got, bound)
			if got > bound {
				t.Fatalf("overwrite Put comparisons=%d exceeds bound=%d (n=%d)", got, bound, n)
			}

			// 3) Get 命中：对堆顶附近与堆底元素各取一次，取较大比较次数。
			var worst int64
			// 命中刚被覆盖更新的 k57（Fix 后可能下沉较深）。
			if hit, err := c.Get("k57"); !hit || err != nil {
				t.Fatalf("Get k57 hit=%v err=%v", hit, err)
			}
			worst = c.lastComparisons()
			// 再命中一个未触碰过的中间键。
			if hit, _ := c.Get(fmt.Sprintf("k%d", n/2)); !hit {
				t.Fatalf("Get k%d missed", n/2)
			}
			if cc := c.lastComparisons(); cc > worst {
				worst = cc
			}
			t.Logf("Get 命中: H 比较最坏 %d 次（上界 %d）", worst, bound)
			if worst > bound {
				t.Fatalf("Get hit comparisons=%d exceeds bound=%d (n=%d)", worst, bound, n)
			}

			// 显式的“非线性”断言：比较次数必须小于 n（随 n 增大时上界对数增长）。
			if got >= int64(n) || worst >= int64(n) {
				t.Fatalf("comparisons grew linearly with n=%d", n)
			}
		})
	}
}

// TestConcurrentAccess 用竞态检测器验证 Put/Get/Peek/Used 可并发调用。
// 结果的线性一致性由互斥锁保证；正确性语义由其它测试覆盖。
func TestConcurrentAccess(t *testing.T) {
	c := mustCache(t, 128)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := fmt.Sprintf("k%d", (id*2000+i)%300)
				if i%4 == 0 {
					c.Get(key)
				} else if i%4 == 1 {
					c.Peek(key)
				} else if i%4 == 2 {
					c.Used()
				} else {
					c.Put(key, 1, int64(1+i%9))
				}
			}
		}(w)
	}
	wg.Wait()
	if c.Used() > 128 {
		t.Fatalf("capacity invariant broken: used=%d", c.Used())
	}
}
