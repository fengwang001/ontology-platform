package lcache

import (
	"strconv"
	"testing"

	"ontology/src"
)

// 按 Key 直接定位：单条事件 / 单次回填访问的条目数不随缓存规模 m 线性增长。
func TestAccessScaling(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := New(m + 1)
		for i := 0; i < m; i++ {
			c.entries["k"+strconv.Itoa(i)] = entry{ver: 1, exists: true}
		}
		c.tracked = m
		c.Apply(src.Event{Key: "k0", Version: 2, Kind: src.Upsert})
		if c.access > 2 {
			t.Fatalf("m=%d: one event touched %d entries", m, c.access)
		}
		if _, err := c.Backfill(src.Token{Key: "k0", Val: "x", Ver: 2, Exists: true}); err != nil {
			t.Fatal(err)
		}
		if c.access > 2 {
			t.Fatalf("m=%d: one backfill touched %d entries", m, c.access)
		}
	}
}

// Delete 事件与 Upsert 事件处理相同：升 fence、删旧条目，但绝不清 fence。
func TestDeleteEventKeepsFence(t *testing.T) {
	c := New(4)
	c.Apply(src.Event{Key: "k", Version: 1, Kind: src.Upsert})
	if _, err := c.Backfill(src.Token{Key: "k", Val: "a", Ver: 1, Exists: true}); err != nil {
		t.Fatal(err)
	}
	c.Apply(src.Event{Key: "k", Version: 2, Kind: src.Delete})
	if c.Fence("k") != 2 {
		t.Fatalf("fence cleared by Delete event: %d", c.Fence("k"))
	}
	if _, _, _, has := c.Snapshot("k"); has {
		t.Fatal("stale entry not evicted by Delete event")
	}
	// 旧读取（v1）在 Delete 之后到达：必须被拒。
	ok, err := c.Backfill(src.Token{Key: "k", Val: "a", Ver: 1, Exists: true})
	if err != nil || ok {
		t.Fatal("stale backfill accepted after Delete event")
	}
	// 重复 / 过时事件什么都不做。
	c.Apply(src.Event{Key: "k", Version: 2, Kind: src.Delete})
	c.Apply(src.Event{Key: "k", Version: 1, Kind: src.Upsert})
	if c.Fence("k") != 2 {
		t.Fatalf("fence moved by stale event: %d", c.Fence("k"))
	}
}
