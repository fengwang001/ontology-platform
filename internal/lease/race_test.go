package lease

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentContention：N 个 goroutine 在共享逻辑时钟上反复
// 抢占、续约、写入、释放。由于 token 严格单调且 Write 只接受当前
// 有效租约的 token，成功的写必然按 token 升序串行落盘，因此最终
// 资源内容必须等于 "最大 token 的成功写" 的值；任何被拒绝的写
// （旧 token、伪造 token）都不得留下痕迹。
func TestConcurrentContention(t *testing.T) {
	var clock atomic.Int64
	m := New(clock.Load)
	const goroutines = 8
	const iterations = 300
	var mu sync.Mutex
	var bestToken uint64
	bestVal := ""
	accepted := 0
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			holder := fmt.Sprintf("g%d", id)
			for i := 0; i < iterations; i++ {
				// 推进时钟制造自然过期，让别人有机会抢占。
				clock.Add(1)
				token, err := m.Acquire(holder, 5)
				if err != nil {
					// 没抢到：伪造巨大 token 写，必须被 fencing
					// 拒绝且分类为 ErrUnknownToken。
					if werr := m.Write(1<<60, "k", "forged"); !errors.Is(werr, ErrUnknownToken) {
						t.Errorf("forged Write = %v, want ErrUnknownToken", werr)
					}
					continue
				}
				val := fmt.Sprintf("%s/token-%d", holder, token)
				if werr := m.Write(token, "k", val); werr == nil {
					mu.Lock()
					accepted++
					if token > bestToken {
						bestToken, bestVal = token, val
					}
					mu.Unlock()
				}
				_ = m.Renew(holder, token, 5)
				_ = m.Release(holder, token)
				// 释放（或时钟被他人推进导致自然过期）后，旧 token
				// 必须被 fencing 拒绝：已被取代报 ErrStaleToken，
				// 无人抢占但已过期报 ErrLeaseExpired，两者都合法。
				werr := m.Write(token, "k", "stale")
				if !errors.Is(werr, ErrStaleToken) && !errors.Is(werr, ErrLeaseExpired) {
					t.Errorf("post-release Write = %v, want fencing rejection", werr)
				}
			}
		}(g)
	}
	wg.Wait()
	if accepted == 0 {
		t.Fatal("no write was ever accepted; test is vacuous")
	}
	got, ok := m.Read("k")
	if !ok || got != bestVal {
		t.Fatalf("final Read = (%q, %v), want (%q, true)", got, ok, bestVal)
	}
}

// TestConcurrentAcquireSingleWinner：同一时刻多个 goroutine 抢一个
// 空闲租约，必须恰好一个成功，且 token 无重复。
func TestConcurrentAcquireSingleWinner(t *testing.T) {
	var clock atomic.Int64
	m := New(clock.Load)
	const goroutines = 16
	tokens := make(chan uint64, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if tok, err := m.Acquire(fmt.Sprintf("g%d", id), 1<<40); err == nil {
				tokens <- tok
			}
		}(g)
	}
	wg.Wait()
	close(tokens)
	seen := map[uint64]bool{}
	count := 0
	for tok := range tokens {
		if seen[tok] {
			t.Fatalf("duplicate token %d", tok)
		}
		seen[tok] = true
		count++
	}
	if count != 1 {
		t.Fatalf("%d goroutines acquired the lease, want exactly 1", count)
	}
}
