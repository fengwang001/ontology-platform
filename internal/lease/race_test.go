package lease

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentContention 在 -race 下验证并发抢占：
// N 个 goroutine 反复抢占、续约、写入，并穿插用伪造 token 做恶意写入。
// 结束后断言：
//   - 资源内容一定是最后一个有效持有者写入的值；
//   - 所有被拒绝的写没有留下任何痕迹；
//   - 所有成功 Acquire 发出的 token 互不相同（严格单调的必然推论）。
func TestConcurrentContention(t *testing.T) {
	var tick atomic.Int64
	m := New(tick.Load)

	const workers = 8
	const rounds = 200

	var mu sync.Mutex
	tokens := make(map[uint64]string) // token -> holder，查重
	var writes atomic.Int64           // 成功写入总数

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			holder := fmt.Sprintf("H%d", id)

			for r := 0; r < rounds; r++ {
				// 推进逻辑时钟，制造过期与抢占窗口。
				tick.Add(1)

				tok, err := m.Acquire(holder, 5)
				if err != nil {
					// 抢占失败：用伪造 token 尝试写，必须被拒且不留痕迹。
					if werr := m.Write(^uint64(0), "evil", holder); werr == nil {
						t.Errorf("伪造 token 写入竟然成功")
					}
					continue
				}

				mu.Lock()
				if prev, dup := tokens[tok]; dup {
					t.Errorf("token %d 重复发出（上次属于 %s）", tok, prev)
				}
				tokens[tok] = holder
				mu.Unlock()

				// Acquire 返回后租约可能已被并发推进的时钟过期，
				// 此时写被拒是合法行为；只有写成功才计数。
				if werr := m.Write(tok, "owner", holder); werr == nil {
					writes.Add(1)
				}
				_ = m.Renew(holder, tok, 5)
				if r%3 == 0 {
					_ = m.Release(holder, tok)
				}
			}
		}(i)
	}
	wg.Wait()

	// 伪造 token 的写绝不能留下痕迹。
	if v, ok := m.Read("evil"); ok {
		t.Fatalf("被拒绝的写留下痕迹: evil=%q", v)
	}
	if writes.Load() == 0 {
		t.Fatal("没有任何一次有效写入成功")
	}

	// 最终阶段：确定性地验证"最后一个有效持有者"的写生效。
	tick.Add(1000) // 让所有在途租约全部过期
	tok, err := m.Acquire("final", 1000)
	if err != nil {
		t.Fatalf("最终 Acquire: %v", err)
	}
	if err := m.Write(tok, "owner", "final"); err != nil {
		t.Fatalf("最终写入: %v", err)
	}
	if v, ok := m.Read("owner"); !ok || v != "final" {
		t.Fatalf("owner = %q,%v, want final,true", v, ok)
	}
}

// TestConcurrentAcquireSingleWinner 验证多个 goroutine 同时抢同一个
// 空闲租约时只有一个成功。
func TestConcurrentAcquireSingleWinner(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		var tick atomic.Int64
		m := New(tick.Load)

		const n = 16
		var wg sync.WaitGroup
		var wins atomic.Int64
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				if _, err := m.Acquire(fmt.Sprintf("H%d", id), 1000); err == nil {
					wins.Add(1)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if got := wins.Load(); got != 1 {
			t.Fatalf("trial %d: 成功者 %d 个, want 1", trial, got)
		}
	}
}
