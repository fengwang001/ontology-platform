package ontology

import (
	"testing"
)

func drainAll(s *Subscription) []int64 {
	var seqs []int64
	for {
		d, err := s.Receive()
		if err != nil {
			return seqs
		}
		seqs = append(seqs, d.Seq)
	}
}

func publishN(t *testing.T, d *Dispatcher, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
}

// DropOldest: the newest messages survive; dropped seqs are 1..n-buffer.
func TestOverflowDropOldest(t *testing.T) {
	d := NewDispatcher()
	s, err := d.Subscribe(SubscribeOptions{Buffer: 3, OnOverflow: DropOldest, OnCancel: CancelDrain})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 5)
	if got := s.DroppedTotal(); got != 2 {
		t.Fatalf("dropped total = %d, want 2", got)
	}
	if got := s.LastDroppedSeq(); got != 2 {
		t.Fatalf("last dropped seq = %d, want 2", got)
	}
	d.Unsubscribe(s)
	seqs := drainAll(s)
	want := []int64{3, 4, 5}
	if len(seqs) != len(want) {
		t.Fatalf("received %v, want %v", seqs, want)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("received %v, want %v", seqs, want)
		}
	}
}

// DropNewest: queued messages are the first ones; later messages are dropped.
func TestOverflowDropNewest(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 2, OnOverflow: DropNewest, OnCancel: CancelDrain})
	publishN(t, d, 5)
	if got := s.DroppedTotal(); got != 3 {
		t.Fatalf("dropped total = %d, want 3", got)
	}
	if got := s.LastDroppedSeq(); got != 5 {
		t.Fatalf("last dropped seq = %d, want 5", got)
	}
	d.Unsubscribe(s)
	seqs := drainAll(s)
	want := []int64{1, 2}
	if len(seqs) != 2 || seqs[0] != want[0] || seqs[1] != want[1] {
		t.Fatalf("received %v, want %v", seqs, want)
	}
}

// DropNewestAndDisconnect: subscriber is marked lagging, removed, and only
// already queued messages can be drained.
func TestOverflowDisconnect(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 2, OnOverflow: DropNewestAndDisconnect})
	publishN(t, d, 4)
	if s.Active() {
		t.Fatal("subscriber should be inactive")
	}
	if got := s.DroppedTotal(); got != 1 {
		t.Fatalf("dropped total = %d, want 1", got)
	}
	if got := s.LastDroppedSeq(); got != 3 {
		t.Fatalf("last dropped seq = %d, want 3", got)
	}
	seqs := drainAll(s) // disconnected => drain buffered then EOF
	want := []int64{1, 2}
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("received %v, want %v", seqs, want)
	}
	if infos := d.SubscribersFor(Change{Entity: "e", Attribute: "a"}); len(infos) != 0 {
		t.Fatalf("disconnected subscriber still registered: %v", infos)
	}
}

// Within one Publish, a full DropOldest subscriber dropping must not affect
// a second subscriber receiving the complete stream.
func TestOverflowIsolation(t *testing.T) {
	d := NewDispatcher()
	slow, _ := d.Subscribe(SubscribeOptions{Buffer: 1, OnOverflow: DropOldest, OnCancel: CancelDrain})
	fast, _ := d.Subscribe(SubscribeOptions{Buffer: 100, OnOverflow: DropNewest, OnCancel: CancelDrain})
	publishN(t, d, 20)
	if got := fast.DroppedTotal(); got != 0 {
		t.Fatalf("fast subscriber dropped %d, want 0", got)
	}
	d.Unsubscribe(fast)
	seqs := drainAll(fast)
	if len(seqs) != 20 {
		t.Fatalf("fast subscriber got %d messages, want 20", len(seqs))
	}
	d.Unsubscribe(slow)
}
