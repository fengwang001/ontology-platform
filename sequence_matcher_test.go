package ontology

import "testing"

// Received sequences must be strictly increasing; every gap must correspond
// exactly to a dropped message, both in count and position.
func TestSequenceGapsMatchDrops(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 2, OnOverflow: DropOldest, OnCancel: CancelDrain})
	publishN(t, d, 10)
	d.Unsubscribe(s)
	seqs := drainAll(s)
	// Buffer holds the last two: 9, 10; dropped 1..8.
	if len(seqs) != 2 || seqs[0] != 9 || seqs[1] != 10 {
		t.Fatalf("received %v, want [9 10]", seqs)
	}
	if got := s.DroppedTotal(); got != 8 {
		t.Fatalf("dropped = %d, want 8", got)
	}

	// Gap intervals are derivable from received seqs + total drops.
	missing := gapCount(seqs, 1, 10)
	if missing != s.DroppedTotal() {
		t.Fatalf("gap count %d != dropped count %d", missing, s.DroppedTotal())
	}

	// Recompute with a DropNewest subscriber: gaps sit at the tail.
	d2 := NewDispatcher()
	s2, _ := d2.Subscribe(SubscribeOptions{Buffer: 3, OnOverflow: DropNewest, OnCancel: CancelDrain})
	publishN(t, d2, 10)
	d2.Unsubscribe(s2)
	got := drainAll(s2)
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("received %v, want [1 2 3]", got)
	}
	if gapCount(got, 1, 10) != s2.DroppedTotal() || s2.DroppedTotal() != 7 {
		t.Fatalf("gaps/drops inconsistent: gaps=%d drops=%d", gapCount(got, 1, 10), s2.DroppedTotal())
	}
}

func gapCount(received []int64, first, last int64) int64 {
	seen := make(map[int64]bool, len(received))
	for _, v := range received {
		seen[v] = true
	}
	var n int64
	for v := first; v <= last; v++ {
		if !seen[v] {
			n++
		}
	}
	return n
}

func TestStrictlyIncreasingUnderMix(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 4, OnOverflow: DropOldest, OnCancel: CancelDrain})
	for i := 0; i < 50; i++ {
		d.Publish(Change{Entity: "e", Attribute: "a"})
	}
	d.Unsubscribe(s)
	seqs := drainAll(s)
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("sequences not strictly increasing: %v", seqs)
		}
	}
}

func TestPrefixNotSubstring(t *testing.T) {
	cases := []struct {
		prefix, entity string
		want           bool
	}{
		{"user", "user", true},
		{"user", "user.1", true},
		{"user", "user.1.name", true},
		{"user", "superuser", false},
		{"user", "superuser.x", false},
		{"user", "user2", false},
		{"user", "use", false},
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := matchPrefix(c.prefix, c.entity); got != c.want {
			t.Errorf("matchPrefix(%q,%q)=%v want %v", c.prefix, c.entity, got, c.want)
		}
	}
}

func TestAttributeExactMatch(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{
		Buffer: 10, OnCancel: CancelDrain,
		EntityPrefix: "user",
		Attributes:   []string{"name"},
	})
	d.Publish(Change{Entity: "user.1", Attribute: "name"})
	d.Publish(Change{Entity: "user.1", Attribute: "username"}) // no fuzzy match
	d.Publish(Change{Entity: "superuser", Attribute: "name"})  // prefix boundary
	d.Unsubscribe(s)
	seqs := drainAll(s)
	if len(seqs) != 1 || seqs[0] != 1 {
		t.Fatalf("received %v, want only seq 1", seqs)
	}
}

func TestEmptyAttributeSetMatchesAll(t *testing.T) {
	d := NewDispatcher()
	s, _ := d.Subscribe(SubscribeOptions{Buffer: 10, OnCancel: CancelDrain})
	d.Publish(Change{Entity: "x", Attribute: "a"})
	d.Publish(Change{Entity: "y", Attribute: "b"})
	d.Unsubscribe(s)
	if seqs := drainAll(s); len(seqs) != 2 {
		t.Fatalf("received %v, want both", seqs)
	}
}

func TestSubscribersForStableOrder(t *testing.T) {
	d := NewDispatcher()
	s1, _ := d.Subscribe(SubscribeOptions{Buffer: 1, EntityPrefix: "a"})
	s2, _ := d.Subscribe(SubscribeOptions{Buffer: 1, EntityPrefix: "a", Attributes: []string{"z", "a"}})
	s3, _ := d.Subscribe(SubscribeOptions{Buffer: 1, EntityPrefix: "b"})
	infos := d.SubscribersFor(Change{Entity: "a", Attribute: "z"})
	if len(infos) != 2 || infos[0].ID != s1.id || infos[1].ID != s2.id {
		t.Fatalf("unexpected/order infos: %+v", infos)
	}
	if len(infos[1].Attributes) != 2 || infos[1].Attributes[0] != "a" || infos[1].Attributes[1] != "z" {
		t.Fatalf("attributes not sorted: %v", infos[1].Attributes)
	}
	_ = s3
}
