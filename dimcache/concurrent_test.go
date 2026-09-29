package dimcache

import (
	"context"
	"sync"
	"testing"
)

// 并发场景：查询、两步读取与回填、源头更新、事件投递同时进行；
// 要求 -race 下无竞态，收敛后每个被跟踪键与无缓存直读源头一致。
func TestConcurrentReadersWritersDeliveries(t *testing.T) {
	c, src, q := newSystem(t, 0)
	ctx := context.Background()

	const writers = 4
	const readers = 8
	const rounds = 60

	var wg sync.WaitGroup
	var trackedMu sync.Mutex
	tracked := map[string]struct{}{}
	track := func(k string) {
		trackedMu.Lock()
		tracked[k] = struct{}{}
		trackedMu.Unlock()
	}

	// 源头更新 + 事件入队（事件故意延迟：入队不等于立即投递）。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				key := keyName((id + r) % 12)
				track(key)
				rec, _ := src.Read(ctx, key)
				if err := src.Update(ctx, key, val(id, r)); err != nil {
					t.Errorf("update: %v", err)
					return
				}
				if err := q.Enqueue(Event{
					Key: key, Version: rec.Version + 1, Kind: EventUpsert, Value: val(id, r),
				}); err != nil {
					t.Errorf("enqueue: %v", err)
					return
				}
				// 偶发删除。
				if r%7 == 3 {
					if err := src.Delete(ctx, key); err != nil {
						t.Errorf("delete: %v", err)
						return
					}
					if err := q.Enqueue(Event{Key: key, Version: rec.Version + 2, Kind: EventDelete}); err != nil {
						t.Errorf("enqueue delete: %v", err)
						return
					}
				}
			}
		}(w)
	}

	// 读取者：查询 + 两步读取/回填（令牌始终消费掉）。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				key := keyName((id + n) % 12)
				track(key)
				_, _, _ = c.Query(ctx, key)
				tok, err := c.IssueReadToken(ctx, key)
				if err != nil {
					t.Errorf("issue: %v", err)
					return
				}
				_ = c.Backfill(ctx, tok) // 过期是合法结果
			}
		}(r)
	}

	// 投递者：持续排空队列；并夹杂重复/更旧事件投递。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < rounds*writers*2; n++ {
			_, _ = c.DrainDeliver(ctx)
			_ = c.Deliver(Event{Key: keyName(n % 12), Version: int64(1 + n%3), Kind: EventUpsert})
		}
	}()

	wg.Wait()

	// 排空剩余事件；读取者的令牌均已同步回填，静止后逐一校验。
	for {
		ok, err := c.DrainDeliver(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	if !c.Quiescent() {
		t.Fatalf("not quiescent: queue=%d pending=%d", q.Len(), c.PendingTokens())
	}
	// 静止后对仍未缓存的键再做一次两步读取（真实系统中的未命中回源），
	// 此时令牌版本必不低于栅栏，回填必然被接受。
	for k := range tracked {
		if _, ok := c.SnapshotEntry(k); !ok {
			tok, err := c.IssueReadToken(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Backfill(ctx, tok); err != nil {
				t.Fatalf("final backfill %q: %v", k, err)
			}
		}
	}
	for k := range tracked {
		assertEntryMatchesSource(t, c, src, k)
	}
}

func keyName(i int) string {
	return "k" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func val(id, r int) string {
	return "v" + itoa(id) + "-" + itoa(r)
}
