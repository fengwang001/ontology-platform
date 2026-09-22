package replica

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ontology/bus"
	"ontology/single"
	"ontology/version"
)

// 规则 9：应用通知只触及受影响的键，检查数不随总条目数增长。
func TestCheckedEntriesIndependentOfCacheSize(t *testing.T) {
	for _, n := range []int{100, 10000} {
		b := newBackend()
		r, _ := newTestReplica(b, 1<<40)
		for i := 0; i < n; i++ {
			mustGet(t, r, fmt.Sprintf("k%d", i))
		}
		before := r.Stats().CheckedEntries
		deliver(r, []bus.Notification{{Key: "k0", Version: 2}})
		got := r.Stats().CheckedEntries - before
		if got != 1 {
			t.Fatalf("N=%d: checked %d entries for 1-key notification, want 1", n, got)
		}
	}
}

// 规则 10：条目数超限时淘汰 LRU（最近最少使用）条目。
func TestEntryLimitEvictsLRU(t *testing.T) {
	b := newBackend()
	clock := NewManualClock()
	r := New(Config{TTL: 1 << 40, MaxEntries: 2, Loader: b.load, Clock: clock.Clock()})
	mustGet(t, r, "a")
	mustGet(t, r, "b")
	mustGet(t, r, "c") // a 最久未用，应被淘汰
	if r.Inspect("a").Exists {
		t.Fatal("a should have been evicted (LRU)")
	}
	if !r.Inspect("b").Exists || !r.Inspect("c").Exists {
		t.Fatal("b and c must survive")
	}
	mustGet(t, r, "b") // 刷新 b 的 LRU 序号
	mustGet(t, r, "d") // 现在 c 最久未用
	if r.Inspect("c").Exists {
		t.Fatal("c should have been evicted after b was touched")
	}
	if !r.Inspect("b").Exists || !r.Inspect("d").Exists {
		t.Fatal("b and d must survive")
	}
}

// 规则 10：全部条目都在回源中时拒绝新条目，且不改变任何已有状态。
func TestEntryLimitRejectKeepsStateUntouched(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		close(started)
		<-release
		return "v", 1, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 1 << 40, MaxEntries: 1, Loader: loader, Clock: clock.Clock()})
	go r.Get(context.Background(), "pinned")
	<-started // "pinned" 条目已接纳且在回源中，不可淘汰

	before := r.Inspect("pinned")
	statsBefore := r.Stats()
	_, _, err := r.Get(context.Background(), "newcomer")
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("want ErrTooManyEntries, got %v", err)
	}
	if r.Inspect("newcomer").Exists {
		t.Fatal("rejected key must not create an entry")
	}
	if after := r.Inspect("pinned"); after != before {
		t.Fatalf("rejection changed existing entry: %+v -> %+v", before, after)
	}
	if after := r.Stats(); after != statsBefore {
		t.Fatalf("rejection changed stats: %+v -> %+v", statsBefore, after)
	}
	close(release)
}

// 规则 10：单飞并发超限拒绝，且不改变任何已有状态。
func TestInflightLimitRejectKeepsStateUntouched(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if key == "slow" {
			close(started)
			<-release
		}
		return "v", 1, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 1 << 40, MaxInflight: 1, Loader: loader, Clock: clock.Clock()})
	go r.Get(context.Background(), "slow")
	<-started
	statsBefore := r.Stats()
	_, _, err := r.Get(context.Background(), "other")
	if !errors.Is(err, single.ErrTooManyInflight) {
		t.Fatalf("want ErrTooManyInflight, got %v", err)
	}
	if after := r.Stats(); after != statsBefore {
		t.Fatalf("rejection changed stats: %+v -> %+v", statsBefore, after)
	}
	close(release)
}

// 规则 10：三类超限错误彼此可判定。
func TestLimitErrorsDistinguishable(t *testing.T) {
	if errors.Is(ErrTooManyEntries, single.ErrTooManyInflight) ||
		errors.Is(ErrTooManyEntries, bus.ErrQueueFull) ||
		errors.Is(single.ErrTooManyInflight, bus.ErrQueueFull) {
		t.Fatal("three limit errors must be distinguishable")
	}
}
