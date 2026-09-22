package replica

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/bus"
	"ontology/version"
)

// 规则 12：并发读、并发投递通知、并发推进时钟（过期）下
// go test -race 必须干净，且不得死锁。
func TestConcurrentReadersPublishersAndExpiry(t *testing.T) {
	b := newBackend()
	for i := 0; i < 8; i++ {
		b.write(fmt.Sprintf("k%d", i), "v1")
	}
	clock := NewManualClock()
	r := New(Config{TTL: 50, MaxEntries: 64, MaxInflight: 4, Loader: b.load, Clock: clock.Clock()})
	bp := bus.New(0)
	r.Attach(bp)
	const workers = 12
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%d", rng.Intn(8))
				switch rng.Intn(3) {
				case 0:
					r.Get(context.Background(), key)
				case 1:
					r.Inspect(key)
				case 2:
					r.Stats()
				}
			}
		}(int64(w))
	}
	// 并发投递：乱序、重复、丢失。
	for p := 0; p < 200; p++ {
		key := fmt.Sprintf("k%d", p%8)
		v := b.write(key, fmt.Sprintf("v%d", p))
		if p%7 == 0 {
			continue // 模拟丢失
		}
		n := bus.Notification{Key: key, Version: version.Version(v)}
		if err := bp.Publish(n); err != nil {
			t.Fatal(err)
		}
		if p%5 == 0 {
			if err := bp.Publish(n); err != nil { // 重复投递
				t.Fatal(err)
			}
		}
		if p%3 == 0 {
			bp.Flush()
		}
		if p%11 == 0 {
			clock.Advance(10) // 推进时钟制造过期
		}
	}
	bp.Flush()
	close(stop)
	wg.Wait()
	// 全部通知投递完毕并过期一轮后，读必须收敛到后端当前版本。
	clock.Advance(50)
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("k%d", i)
		mustGet(t, r, key)
		if got, want := r.Inspect(key).Version, b.ver; got != want {
			t.Fatalf("%s: version %v want %v", key, got, want)
		}
	}
}
