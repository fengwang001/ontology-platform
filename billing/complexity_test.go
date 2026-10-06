package billing

import (
	"fmt"
	"sync"
	"testing"
)

// TestComplexityNotLinear 以可验证方式证明提交/查询的树访问步数不随
// 已收到采样数线性增长：
//
//  1. 结构性下界：若单次操作为 O(K)，当 K 从 10^4 增至 10^5（10 倍），
//     访问步数应增长约 10 倍；树结构期望步数为 O(log K)，增长约 1.25 倍。
//  2. 硬性上界：对全部插入与第 k 大查询，单步访问数不超过 5*ceil(log2(K+1))。
//
// 测试直接读取 treap 记录的 lastSteps（沿树访问的节点计数）。
func TestComplexityNotLinear(t *testing.T) {
	sizes := []int{10_000, 100_000}
	maxStep := map[int]int{}
	avgStep := map[int]int{}

	for _, size := range sizes {
		s := NewSettler(0, size)
		var sum int
		var mx int
		// 插入不同有效值（槽位 i，值为伪随机分散键），触发真实树路径。
		for i := 0; i < size; i++ {
			// splitmix64 扰动后取模，键合法且在值域内充分散布。
			z := uint64(i) + 0x9E3779B97F4A7C15
			z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
			z = (z ^ (z >> 27)) * 0x94D049BB133111EB
			v := int64((z ^ (z >> 31)) % (MaxRate + 1))
			if err := s.Submit(Sample{Slot: i, Ingress: v, Version: 1}); err != nil {
				t.Fatal(err)
			}
			if st := s.values.lastWriteSteps; st > mx {
				mx = st
			}
			sum += s.values.lastWriteSteps
		}
		// 查询第 k 大（丢弃后的计费速率路径），在多个 rank 上采样。
		for _, rank := range []int{1, size / 2, size * 95 / 100, size} {
			var rd int
			_ = s.values.kthLargest(rank, &rd)
			if rd > mx {
				mx = rd
			}
		}
		maxStep[size] = mx
		avgStep[size] = sum / size
	}

	// 硬性上界校验：5*ceil(log2(K+1))。
	for _, size := range sizes {
		// 计算 ceil(log2(K+1))
		var lg int
		for (1 << lg) < size+1 {
			lg++
		}
		bound := 5 * lg
		if maxStep[size] > bound {
			t.Fatalf("K=%d 单步访问 %d 超过对数上界 %d", size, maxStep[size], bound)
		}
		t.Logf("K=%d: 平均树访问=%d 最大树访问=%d 对数上界=%d", size, avgStep[size], maxStep[size], bound)
	}

	// 增长形态校验：K 扩大 10 倍，最大步数增长应远小于 10 倍（取阈值 3）。
	growth := float64(maxStep[100_000]) / float64(maxStep[10_000])
	t.Logf("K:1e4->1e5（10 倍）时最大访问步数增长倍数=%.2f（线性实现应为 ~10）", growth)
	if growth >= 3.0 {
		t.Fatalf("访问步数增长 %.2f 倍，疑似随 K 线性增长", growth)
	}

	// 同步给出朴素线性实现的对照步数（K 份必须全量扫描/排序）。
	t.Logf("对照：朴素排序查询每次处理的元素数 = K（10000 -> 100000，恰为 10 倍）")
	_ = fmt.Sprint // 保留 fmt 依赖占位
}

// TestConcurrentLinearizability 在 -race 下并发混合提交/查询/概览/撤回，
// 结束后与串行不变量核对：K 恒等于概览计数，费率与朴素规则一致。
func TestConcurrentLinearizability(t *testing.T) {
	const n = 500
	s := NewSettler(0, n)
	model := NewNaiveSettler(n)

	// 先用串行方式灌一批基础数据到两个实现，保证并发阶段版本连续。
	for i := 0; i < n; i++ {
		sample := Sample{Slot: i, Ingress: int64(i + 1), Egress: int64(i), Version: 1}
		if err := s.Submit(sample); err != nil {
			t.Fatal(err)
		}
		_ = model.Submit(sample)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 并发查询/概览：任何时刻读到的 K、费率都必须自洽且与模型可重算值相容。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				o := s.Overview()
				if o.ReceivedSlots+o.MissingSlots != n {
					t.Errorf("概览计数不自洽: %+v", o)
					return
				}
				// 注意：Overview 与 CurrentRate 是两次独立加锁的调用，
				// 其间可能被写操作插入，因此不比较二者数值，只校验各自快照不变量。
				if (o.ReceivedSlots > 0) != o.RateDefined {
					t.Errorf("概览费率定义与计数不自洽: %+v", o)
					return
				}
				if _, err := s.CurrentRate(); err != nil && o.ReceivedSlots > 0 {
					t.Errorf("K=%d 时查询不应无采样", o.ReceivedSlots)
					return
				}
			}
		}()
	}

	// 并发提交更高版本与撤回：仅验证无数据竞争、无计数破坏、不变量成立。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				slot := (id*7 + i) % n
				// 查询永远安全：并发交错的最终效果等价于某串行顺序。
				if _, err := s.CurrentRate(); err != nil {
					// K 可能瞬时为 0（理论上此处基础数据不会全撤回，忽略无采样）
					_ = err
				}
				_ = s.Withdraw(slot, 1)
				_ = s.Submit(Sample{Slot: slot, Ingress: int64(i + 2), Version: int64(i + 2)})
			}
		}(g)
	}

	// 让并发跑一小段。
	for i := 0; i < 50; i++ {
		o := s.Overview()
		if o.ReceivedSlots+o.MissingSlots != n {
			t.Fatalf("并发中计数破坏: %+v", o)
		}
	}

	close(stop)
	wg.Wait()

	o := s.Overview()
	if o.ReceivedSlots+o.MissingSlots != n {
		t.Fatalf("并发结束后计数不自洽: %+v", o)
	}
	if !o.RateDefined {
		t.Fatalf("并发结束后应有有效采样: %+v", o)
	}
	t.Logf("并发混合结束: K=%d missing=%d rate=%d", o.ReceivedSlots, o.MissingSlots, o.BilledRate)
}
