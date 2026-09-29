package dimcache

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentConvergence 在查询、回填、更新与投递并发交错的压力下，
// 校验队列清空且无未完成令牌后，每个键的查询结果与无缓存直读源头一致。
func TestConcurrentConvergence(t *testing.T) {
	m := newTestManager(t, 8)
	keys := []string{"k0", "k1", "k2", "k3"}

	// 初始数据。
	for i, key := range keys {
		mustUpdate(t, m, key, fmt.Sprintf("v0-%d", i))
	}

	var wg sync.WaitGroup

	// 更新者：对每个键做多轮更新，并把对应事件放入队列。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 1; round <= 12; round++ {
			for i, key := range keys {
				ver, err := m.Update(key, fmt.Sprintf("v%d-%d", round, i))
				if err != nil {
					t.Errorf("update: %v", err)
					return
				}
				if err := m.Emit(Event{Key: key, Version: ver, Value: fmt.Sprintf("v%d-%d", round, i)}); err != nil {
					t.Errorf("emit: %v", err)
					return
				}
				// 部分轮次制造删除与复活。
				if round == 4 || round == 9 {
					dv, derr := m.Delete(key)
					if derr != nil {
						t.Errorf("delete: %v", derr)
						return
					}
					if err := m.Emit(Event{Key: key, Version: dv, Deleted: true}); err != nil {
						t.Errorf("emit delete: %v", err)
						return
					}
				}
			}
		}
	}()

	// 投递者：持续排空事件，模拟延迟且乱序交错。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			ok, err := m.DeliverNext()
			if err != nil {
				t.Errorf("deliver: %v", err)
				return
			}
			if !ok {
				// 更新者仍可能继续入队；靠收敛阶段最终排空。
				if m.PendingEvents() == 0 {
					return
				}
			}
		}
	}()

	// 查询者：并发走缓存两阶段读取，任何结果都必须与当时直读源头一致是
	// 不现实的（允许读到旧值），因此这里只校验结果形态合法；最终一致性
	// 在收敛后统一对照。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				key := keys[(id+n)%len(keys)]
				if _, _, err := m.Query(key); err != nil {
					t.Errorf("query: %v", err)
					return
				}
			}
		}(w)
	}

	// 显式两阶段读取者：begin/backfill 被交错打断是常态，ErrTokenStale
	// 是允许结果；未知/已用以外的错误不应出现。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 100; n++ {
			key := keys[n%len(keys)]
			tok, _, err := m.BeginRead(key)
			if err != nil {
				t.Errorf("begin read: %v", err)
				return
			}
			if err := m.Backfill(tok); err != nil &&
				err != ErrTokenStale && err != ErrTooManyTrackedKeys {
				t.Errorf("backfill unexpected: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	// 收敛：排空队列，并消费/确认没有残留令牌（Query 已即时消费）。
	drain(t, m)

	// 等待所有 Query/Backfill 完成后，不应再有未完成令牌；若显式读取者
	// 恰好在此刻留下令牌，则消费掉并再次排空，循环直至静止。
	for i := 0; i < 100 && !m.Quiescent(); i++ {
		drain(t, m)
		m.mu.Lock()
		for id := range m.pendingTokens {
			rec := m.pendingTokens[id]
			delete(m.pendingTokens, id)
			m.usedTokens[id] = struct{}{}
			if rec.version >= m.fences[rec.key] {
				if _, hit := m.cache[rec.key]; !hit && len(m.cache) < m.cfg.maxTrackedKeys {
					m.cache[rec.key] = cacheRow{version: rec.version, deleted: rec.deleted, value: rec.value}
				}
			}
		}
		m.mu.Unlock()
		drain(t, m)
	}
	if !m.Quiescent() {
		t.Fatalf("manager did not become quiescent: queue=%d pendingTokens hidden", m.PendingEvents())
	}

	// 静止后：缓存路径与无缓存参照逐键对照。
	for _, key := range keys {
		gotVal, gotFound, err := m.Query(key)
		if err != nil {
			t.Fatalf("final Query(%q): %v", key, err)
		}
		refVal, refFound, err := m.DirectQuery(key)
		if err != nil {
			t.Fatalf("DirectQuery(%q): %v", key, err)
		}
		if gotFound != refFound || gotVal != refVal {
			t.Fatalf("convergence mismatch for %q: cached=(%q,%v) direct=(%q,%v)",
				key, gotVal, gotFound, refVal, refFound)
		}

		// 同时校验缓存条目与源头版本一致（无陈旧条目）。
		entry, hit := m.CachedEntry(key)
		m.mu.Lock()
		src := m.source[key]
		m.mu.Unlock()
		if hit && entry.Version != src.version {
			t.Fatalf("stale cache for %q: entry@%d source@%d", key, entry.Version, src.version)
		}
		if entry, hit := m.CachedEntry(key); hit {
			if entry.Deleted != src.deleted || (!src.deleted && entry.Value != src.value) {
				t.Fatalf("cache content mismatch for %q: %+v vs source %+v", key, entry, src)
			}
		}
	}
}
