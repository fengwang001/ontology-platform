package bus

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/version"
)

func collect(b *Bus) *[]version.Version {
	got := &[]version.Version{}
	b.Subscribe(func(n Notification) { *got = append(*got, n.Version) })
	return got
}

func TestPublishFlushFIFO(t *testing.T) {
	b := New(0)
	got := collect(b)
	for v := 1; v <= 5; v++ {
		if err := b.Publish(Notification{Key: "k", Version: version.Version(v)}); err != nil {
			t.Fatal(err)
		}
	}
	if n := b.Flush(); n != 5 {
		t.Fatalf("delivered %d, want 5", n)
	}
	for i, v := range *got {
		if v != version.Version(i+1) {
			t.Fatalf("order broken at %d: %v", i, *got)
		}
	}
	pub, del, drop := b.Stats()
	if pub != 5 || del != 5 || drop != 0 {
		t.Fatalf("stats = %d,%d,%d", pub, del, drop)
	}
}

func TestQueueFullRejectsWithoutStateChange(t *testing.T) {
	b := New(2)
	_ = b.Publish(Notification{Key: "a", Version: 1})
	_ = b.Publish(Notification{Key: "b", Version: 2})
	err := b.Publish(Notification{Key: "c", Version: 3})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v", err)
	}
	if b.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", b.Pending())
	}
}

func TestDuplicateDeliversTwice(t *testing.T) {
	b := New(0)
	got := collect(b)
	if err := b.Duplicate(Notification{Key: "k", Version: 7}); err != nil {
		t.Fatal(err)
	}
	b.Flush()
	if len(*got) != 2 || (*got)[0] != 7 || (*got)[1] != 7 {
		t.Fatalf("got %v", *got)
	}
}

func TestDropOldestSimulatesLoss(t *testing.T) {
	b := New(0)
	got := collect(b)
	_ = b.Publish(Notification{Key: "k", Version: 1})
	_ = b.Publish(Notification{Key: "k", Version: 2})
	if !b.DropOldest() {
		t.Fatal("drop failed")
	}
	b.Flush()
	if len(*got) != 1 || (*got)[0] != 2 {
		t.Fatalf("got %v", *got)
	}
	if b.DropOldest() {
		t.Fatal("empty drop must report false")
	}
}

func TestFlushShuffledDeliversSameSet(t *testing.T) {
	b := New(0)
	got := collect(b)
	for v := 1; v <= 20; v++ {
		_ = b.Publish(Notification{Key: "k", Version: version.Version(v)})
	}
	r := rand.New(rand.NewSource(42))
	if n := b.FlushShuffled(r); n != 20 {
		t.Fatalf("delivered %d", n)
	}
	sorted := append([]version.Version(nil), (*got)...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i, v := range sorted {
		if v != version.Version(i+1) {
			t.Fatalf("set mismatch: %v", sorted)
		}
	}
	if b.Pending() != 0 {
		t.Fatal("queue must be empty after flush")
	}
}

func TestFanoutToManySubscribers(t *testing.T) {
	b := New(0)
	var c1, c2 int
	b.Subscribe(func(Notification) { c1++ })
	b.Subscribe(func(Notification) { c2++ })
	_ = b.Publish(Notification{Key: "k", Version: 1})
	b.Flush()
	if c1 != 1 || c2 != 1 {
		t.Fatalf("fanout = %d,%d", c1, c2)
	}
}
