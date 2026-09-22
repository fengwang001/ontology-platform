package bus

import (
	"errors"
	"testing"

	"ontology/version"
)

func collect(b *Bus) *[]Notification {
	got := &[]Notification{}
	b.Subscribe(func(n Notification) { *got = append(*got, n) })
	return got
}

func TestPublishFlushOrder(t *testing.T) {
	b := New(0)
	got := collect(b)
	for i := 1; i <= 3; i++ {
		if err := b.Publish(Notification{Key: "k", Version: version.Version(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(*got) != 0 {
		t.Fatal("delivery must wait for Flush")
	}
	b.Flush()
	if len(*got) != 3 {
		t.Fatalf("got %d notifications want 3", len(*got))
	}
	for i, n := range *got {
		if n.Version != version.Version(i+1) {
			t.Fatalf("order broken at %d: %v", i, n.Version)
		}
	}
	if b.Pending() != 0 {
		t.Fatal("queue must be empty after Flush")
	}
}

func TestOutOfOrderDuplicateAndDrop(t *testing.T) {
	b := New(0)
	got := collect(b)
	// 乱序注入 v3, v1, v2；重复投递 v1；再丢弃队首一条（v3）。
	mustPublish(t, b, Notification{Key: "k", Version: 3})
	mustPublish(t, b, Notification{Key: "k", Version: 1})
	mustPublish(t, b, Notification{Key: "k", Version: 2})
	mustPublish(t, b, Notification{Key: "k", Version: 1})
	if dropped := b.DropPending(1); dropped != 1 {
		t.Fatalf("dropped=%d want 1", dropped)
	}
	b.Flush()
	want := []uint64{1, 2, 1}
	if len(*got) != len(want) {
		t.Fatalf("got %v want versions %v", *got, want)
	}
	for i, n := range *got {
		if n.Version != version.Version(want[i]) {
			t.Fatalf("at %d got %v want %v", i, n.Version, want[i])
		}
	}
}

func TestQueueFullRejectsWithoutStateChange(t *testing.T) {
	b := New(2)
	mustPublish(t, b, Notification{Key: "a", Version: 1})
	mustPublish(t, b, Notification{Key: "b", Version: 1})
	if err := b.Publish(Notification{Key: "c", Version: 1}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	if b.Pending() != 2 {
		t.Fatal("rejected publish must not change queue")
	}
	// PublishAll 原子性：整体超限则一条都不入队。
	err := b.PublishAll([]Notification{{Key: "d", Version: 1}})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	if b.Pending() != 2 {
		t.Fatal("rejected PublishAll must not change queue")
	}
	got := collect(b)
	b.Flush()
	if len(*got) != 2 || (*got)[0].Key != "a" || (*got)[1].Key != "b" {
		t.Fatalf("queue content changed by rejections: %v", *got)
	}
}

func TestPublishAllAtomicSuccess(t *testing.T) {
	b := New(3)
	mustPublish(t, b, Notification{Key: "a", Version: 1})
	err := b.PublishAll([]Notification{{Key: "b", Version: 1}, {Key: "c", Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Pending() != 3 {
		t.Fatalf("pending=%d want 3", b.Pending())
	}
}

func TestDropPendingMoreThanQueued(t *testing.T) {
	b := New(0)
	mustPublish(t, b, Notification{Key: "a", Version: 1})
	if dropped := b.DropPending(5); dropped != 1 {
		t.Fatalf("dropped=%d want 1", dropped)
	}
	if b.Pending() != 0 {
		t.Fatal("queue must be empty")
	}
}

func mustPublish(t *testing.T, b *Bus, n Notification) {
	t.Helper()
	if err := b.Publish(n); err != nil {
		t.Fatal(err)
	}
}
