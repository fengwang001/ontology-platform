package ontology

import "testing"

// readN 从订阅中恰好读出 n 条消息并返回其序号。
// 队列行为是确定性的，因此这里不需要超时。
func readN(t *testing.T, s *Subscription, n int) []uint64 {
	t.Helper()
	seqs := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		m, ok := <-s.C()
		if !ok {
			t.Fatalf("channel closed after %d/%d messages", i, n)
		}
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func publishN(t *testing.T, d *Dispatcher, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := d.Publish("user1", "name", i); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
}

func TestDropOldest(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 2, Overflow: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 5)
	got := readN(t, s, 2)
	if !equalSeqs(got, []uint64{4, 5}) {
		t.Fatalf("got %v, want [4 5]", got)
	}
	if s.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", s.Dropped())
	}
	if s.LastDroppedSeq() != 3 {
		t.Fatalf("lastDroppedSeq = %d, want 3", s.LastDroppedSeq())
	}
}

func TestDropNewest(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 2, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 5)
	got := readN(t, s, 2)
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got %v, want [1 2]", got)
	}
	if s.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", s.Dropped())
	}
	if s.LastDroppedSeq() != 5 {
		t.Fatalf("lastDroppedSeq = %d, want 5", s.LastDroppedSeq())
	}
}

func TestDisconnect(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 2, Overflow: Disconnect})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 5)
	// 断开后通道关闭，但已在队列中的两条仍可读出。
	got := readN(t, s, 2)
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got %v, want [1 2]", got)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("expected channel to be closed after disconnect")
	}
	if !s.Canceled() {
		t.Fatal("subscription should be marked canceled after disconnect")
	}
	if s.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", s.Dropped())
	}
	if s.LastDroppedSeq() != 3 {
		t.Fatalf("lastDroppedSeq = %d, want 3", s.LastDroppedSeq())
	}
}

// 同一次 Publish 里，一个订阅者的丢弃不得影响另一个订阅者收全。
func TestOverflowPoliciesAreIndependent(t *testing.T) {
	d := New()
	defer d.Close()
	slow, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 1, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := d.Subscribe("user", nil, SubscribeOptions{Buffer: 16, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 5)
	if got := readN(t, slow, 1); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("slow got %v, want [1]", got)
	}
	if slow.Dropped() != 4 {
		t.Fatalf("slow dropped = %d, want 4", slow.Dropped())
	}
	if got := readN(t, fast, 5); !equalSeqs(got, []uint64{1, 2, 3, 4, 5}) {
		t.Fatalf("fast got %v, want [1 2 3 4 5]", got)
	}
	if fast.Dropped() != 0 {
		t.Fatalf("fast dropped = %d, want 0", fast.Dropped())
	}
}
