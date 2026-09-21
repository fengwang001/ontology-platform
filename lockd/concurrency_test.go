package lockd_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/fence"
	"ontology/lease"
)

func TestConcurrentAcquireExactlyOneWinner(t *testing.T) {
	d, _ := newDaemon()
	const n = 32
	var wg sync.WaitGroup
	tokens := make(chan fence.Token, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := d.Acquire("res", fmt.Sprintf("c%d", i), ttl)
			if err == nil {
				tokens <- tok
				return
			}
			if !errors.Is(err, lease.ErrHeld) {
				t.Errorf("loser got %v, want ErrHeld", err)
			}
		}(i)
	}
	wg.Wait()
	close(tokens)
	var got []fence.Token
	for tok := range tokens {
		got = append(got, tok)
	}
	if len(got) != 1 {
		t.Fatalf("%d goroutines won the acquire, want exactly 1", len(got))
	}
	if got[0] != 1 {
		t.Fatalf("winner token = %d, want 1", got[0])
	}
}

func TestConcurrentMixedOperationsRaceClean(t *testing.T) {
	d, c := newDaemon()
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			holder := fmt.Sprintf("c%d", i)
			for round := 0; round < 20; round++ {
				tok, err := d.Acquire("res", holder, ttl)
				if err == nil {
					_ = d.Renew("res", holder, ttl)
					_ = d.Write("res", tok, []byte("x"))
					_ = d.Release("res", holder)
				}
				_ = d.Status("res")
			}
		}(i)
	}
	// 并发推进时钟，过期判定必须依然安全。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			c.Advance(ttl / 10)
		}
	}()
	wg.Wait()
}

func TestConcurrentWriteWatermarkMonotonic(t *testing.T) {
	d, _ := newDaemon()
	tok, err := d.Acquire("res", "alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// 当前令牌的并发写入不应失败。
			if err := d.Write("res", tok, nil); err != nil {
				t.Errorf("write with current token: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			// 过期令牌的并发写入永远应被拒绝，且不得抬动水位。
			if err := d.Write("res", tok-1, nil); !errors.Is(err, fence.ErrStale) {
				t.Errorf("stale write: got %v, want ErrStale", err)
			}
		}()
	}
	wg.Wait()
	// 并发结束后水位不得倒退，当前令牌仍可写。
	if err := d.Write("res", tok, nil); err != nil {
		t.Fatalf("watermark regressed: %v", err)
	}
	// 更高令牌被接受后水位抬升，等于旧水位的令牌也要被拒。
	if err := d.Write("res", tok+1000, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Write("res", tok, nil); !errors.Is(err, fence.ErrStale) {
		t.Fatalf("token at old watermark: got %v, want ErrStale", err)
	}
}
