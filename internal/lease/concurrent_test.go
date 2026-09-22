package lease

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentChaos 是 -race 下的并发测试：N 个 goroutine 反复
// 抢占（Acquire）、续约（Renew）、写入（Write）、释放（Release）。
// 时间只走注入的 now（atomic 逻辑时钟），不靠 sleep 制造时序。
//
// 结束后断言：
//   - 不变量 2：全过程发出的 token 恰好是 1..n，严格递增、无重复、无缺口；
//   - 不变量 4：资源内容一定等于某一次"成功"写入的值（即最后一个有效
//     持有者写下的值），不存在任何被拒绝的写留下的痕迹。
//
// 判别力（改坏方式 -> 失败的测试）：
//   - Manager 各方法的 mu.Lock 删掉（token++ 与 store 写失去互斥）
//     -> -race 直接报警，且 token 序列断言大概率失败。
//   - Write 把 m.store[key] = val 挪到校验之前
//     -> "内容属于成功写集合"断言失败（被拒的写留下痕迹）。
func TestConcurrentChaos(t *testing.T) {
	var clock atomic.Int64
	m := New(func() int64 { return clock.Load() })

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	// 每个 goroutine 只写自己的槽位，主 goroutine 在 Wait 后读取，无数据竞争。
	tokens := make([][]uint64, goroutines) // 各自成功拿到的 token
	writes := make([][]string, goroutines) // 各自成功写入的值

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			holder := fmt.Sprintf("H%d", id)
			var myToken uint64
			for i := 0; i < iterations; i++ {
				clock.Add(1) // 推进逻辑时钟，制造过期与抢占
				switch i % 4 {
				case 0:
					if tok, err := m.Acquire(holder, 20); err == nil {
						myToken = tok
						tokens[id] = append(tokens[id], tok)
					}
				case 1:
					_ = m.Renew(holder, myToken, 20)
				case 2:
					val := fmt.Sprintf("%s/%d/%d", holder, myToken, i)
					if m.Write(myToken, "k", val) == nil {
						writes[id] = append(writes[id], val)
					}
				case 3:
					_ = m.Release(holder, myToken)
				}
			}
		}(g)
	}
	wg.Wait()

	// 不变量 2：所有成功发出的 token 必须恰好是 1..n。
	var all []uint64
	for _, ts := range tokens {
		all = append(all, ts...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	if len(all) == 0 {
		t.Fatal("并发下没有任何一次 Acquire 成功，不符合预期")
	}
	for i, tok := range all {
		if want := uint64(i + 1); tok != want {
			t.Fatalf("token 序列第 %d 个 = %d, 期望 %d：重复、回退或缺口（不变量 2）",
				i, tok, want)
		}
	}

	// 不变量 4：最终内容必须是某次成功写入的值（或从未写入）。
	successful := make(map[string]bool)
	for _, ws := range writes {
		for _, w := range ws {
			successful[w] = true
		}
	}
	if got, ok := m.Read("k"); ok && !successful[got] {
		t.Fatalf("store 内容 %q 不属于任何一次成功写入：被拒绝的写留下了痕迹（不变量 4）", got)
	}

	// 收尾：让一切过期后重新 Acquire，token 必须恰好是 n+1，
	// 且新持有者能正常写读（并发没有把内部状态搞乱）。
	clock.Add(1 << 20)
	final, err := m.Acquire("final", 10)
	if err != nil {
		t.Fatalf("final Acquire: %v", err)
	}
	if want := uint64(len(all)) + 1; final != want {
		t.Fatalf("final token = %d, 期望 %d（不变量 2）", final, want)
	}
	if err := m.Write(final, "k", "final"); err != nil {
		t.Fatalf("final Write: %v", err)
	}
	if v, _ := m.Read("k"); v != "final" {
		t.Fatalf("final Read = %q, 期望 final", v)
	}
}
