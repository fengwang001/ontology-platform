package ontology

import "testing"

func TestRingFIFOWithinCapacity(t *testing.T) {
	r := newRing(3)
	for i := int64(1); i <= 3; i++ {
		r.push(Event{Seq: i})
	}
	if r.len() != 3 {
		t.Fatalf("len = %d, want 3", r.len())
	}
	for i := int64(1); i <= 3; i++ {
		got, ok := r.pop()
		if !ok || got.Seq != i {
			t.Fatalf("pop = (%d,%v), want %d", got.Seq, ok, i)
		}
	}
	if _, ok := r.pop(); ok {
		t.Fatal("pop on empty ring returned ok")
	}
}

func TestRingWrapsAround(t *testing.T) {
	r := newRing(2)
	for cycle := 0; cycle < 5; cycle++ {
		r.push(Event{Seq: int64(cycle*2 + 1)})
		r.push(Event{Seq: int64(cycle*2 + 2)})
		for want := int64(cycle*2 + 1); want <= int64(cycle*2+2); want++ {
			got, ok := r.pop()
			if !ok || got.Seq != want {
				t.Fatalf("cycle %d: got %d ok %v, want %d", cycle, got.Seq, ok, want)
			}
		}
	}
}

func TestRingPeekAndClear(t *testing.T) {
	r := newRing(3)
	r.push(Event{Seq: 10})
	if r.peekOldest().Seq != 10 {
		t.Fatalf("peek = %d, want 10", r.peekOldest().Seq)
	}
	r.clear()
	if r.len() != 0 {
		t.Fatalf("len after clear = %d, want 0", r.len())
	}
	if _, ok := r.pop(); ok {
		t.Fatal("pop after clear returned ok")
	}
}
