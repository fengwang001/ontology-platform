package lease

// 本文件是 -race 下的并发混沌测试（任务第五节）：
// N 个 goroutine 在共享 Manager 上反复 Acquire/Renew/Write/Release。
// 时间完全来自注入的 now（一个只前进、不加锁也安全的 atomic 时钟），
// 全程不使用 sleep。
//
// 结束后断言：
//   - 不变量 2：全过程发出的 token 恰好是 1..K，严格递增、无重复无回退；
//   - 不变量 4：任何被拒绝的写都没有留下痕迹——最终每个键的内容
//     必须来自"某次成功的写"，绝不等于"只被拒绝写过"的值。

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentChaosRace(t *testing.T) {
	var clock atomic.Int64
	m := New(func() int64 { return clock.Load() })

	const (
		n      = 8
		rounds = 200
		ttl    = 4 // 很小：clock 每次前进 1~3，大量出现到期/抢占
	)

	var (
		wg sync.WaitGroup

		mu          sync.Mutex                     // 仅保护下面的观测集合，不参与租约逻辑
		issued      []uint64                       // 每次成功 Acquire 实际拿到的 token
		committed   = map[string]map[string]bool{} // key -> 成功写入的值集合
		rejectedVal = map[string]map[string]bool{} // key -> 仅被拒绝写入的值集合
	)

	noteValue := func(set map[string]map[string]bool, key, val string) {
		if set[key] == nil {
			set[key] = map[string]bool{}
		}
		set[key][val] = true
	}

	// writer 用指定 token 发起一次写。同一个旧号在"仍当前持有者"时
	// 可能合法成功、在被抢占/过期后必然失败，两种结局都记录下来，
	// 由结束后的集合断言判别（而不是预设每次都必须失败）。
	writer := func(tok uint64, g, r int) {
		key := fmt.Sprintf("shared-%d", r%7) // 多个 goroutine 争用同一批键
		val := fmt.Sprintf("g%d-r%d", g, r)
		err := m.Write(tok, key, val)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			noteValue(committed, key, val)
		case errors.Is(err, ErrStaleToken), errors.Is(err, ErrLeaseExpired):
			noteValue(rejectedVal, key, val)
		default:
			t.Errorf("goroutine %d Write 出现未预期错误: %v", g, err)
		}
	}

	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			holder := fmt.Sprintf("h%d", g)
			var staleToken uint64 // 曾经持有过、现已不一定有效的旧号
			for r := 0; r < rounds; r++ {
				clock.Add(1 + int64((g+r)%3)) // 只前进，不 sleep

				tok, err := m.Acquire(holder, ttl)
				if err == nil {
					mu.Lock()
					issued = append(issued, tok)
					mu.Unlock()
					staleToken = tok

					writer(tok, g, r)
					if r%2 == 0 {
						_ = m.Renew(holder, tok, ttl)
						writer(tok, g, r+100000)
					}
					if r%3 == 0 {
						_ = m.Release(holder, tok)
					}
				}
				// 无论本轮是否拿到租约，都用旧号尝试一次写——
				// 专门制造"被抢占者/过期者旧 token"的拒绝流量。
				if staleToken != 0 {
					writer(staleToken, g, r+200000)
				}
			}
		}(g)
	}
	wg.Wait()

	// 断言不变量 2：发号无重复、无回退。
	// 注意 issued 的追加顺序不等于发号顺序（拿到 token 与追加之间
	// goroutine 可能被调度切走），所以这里只断言"集合性质"：
	// 每个号恰好发一次，且发出集合恰好是 {1..maxTok}——计数器
	// 从 1 起每次成功 Acquire 自增 1，重复或回退都会破坏该性质。
	if len(issued) < n {
		t.Fatalf("成功 Acquire 次数 %d 过少，测试未产生有效竞争", len(issued))
	}
	seen := make(map[uint64]bool, len(issued))
	var maxTok uint64
	for _, tok := range issued {
		if seen[tok] {
			t.Fatalf("token %d 重复发出", tok)
		}
		seen[tok] = true
		if tok > maxTok {
			maxTok = tok
		}
	}
	// 计数器从 1 起按 1 递增，故实际发出的集合必须恰好是 {1..maxTok}。
	if uint64(len(issued)) != maxTok {
		t.Fatalf("发出 token 数 %d 与最大 token %d 不符，存在跳号/漏发",
			len(issued), maxTok)
	}

	// 断言不变量 4：最终资源内容只能来自成功的写，
	// 绝不能是任何"只出现在拒绝集合里"的值。
	for key, badVals := range rejectedVal {
		final, ok := m.Read(key)
		if !ok {
			continue // 该键从未被成功创建，拒绝写确实没落盘
		}
		if badVals[final] && !committed[key][final] {
			t.Fatalf("键 %s 最终值 %q 仅来自一次被拒绝的写（不变量 4 被破坏）",
				key, final)
		}
	}

	// 让全部租约过期后再 Acquire 一次，确认终态管理器仍满足互斥：
	// 恰好一个 goroutine 能成功。
	clock.Add(1_000_000)
	var winners int32
	var wg2 sync.WaitGroup
	for g := 0; g < n; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			if _, err := m.Acquire(fmt.Sprintf("final-%d", g), 100); err == nil {
				atomic.AddInt32(&winners, 1)
			}
		}(g)
	}
	wg2.Wait()
	if winners != 1 {
		t.Fatalf("全部过期后并发 Acquire 成功数 = %d, want 1", winners)
	}
}
