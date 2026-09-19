package dispatch

import "testing"

// Prefix matching is segment-aware: "user" matches "user" and "user/alice"
// but never degrades into substring matching ("superuser", "userx").
func TestPrefixNotSubstring(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{EntityPrefix: "user", Capacity: 8, OnFull: DropNewest})

	mustPublish(t, d, "superuser", "a")
	mustPublish(t, d, "userx", "a")
	mustPublish(t, d, "user", "a")
	mustPublish(t, d, "user/alice", "a")

	assertSeqs(t, drainAfterCancel(s), []uint64{3, 4})
}

// Attribute names compare by exact equality; an empty set matches all.
func TestAttrExactAndEmptySet(t *testing.T) {
	d := New()
	defer d.Close()
	named := mustSubscribe(t, d, Options{Attrs: []string{"name", "age"}, Capacity: 8, OnFull: DropNewest})
	all := mustSubscribe(t, d, Options{Capacity: 8, OnFull: DropNewest})

	mustPublish(t, d, "e", "name")
	mustPublish(t, d, "e", "Name") // case differs: no match for named
	mustPublish(t, d, "e", "nam")  // prefix of name: no match for named

	assertSeqs(t, drainAfterCancel(named), []uint64{1})
	assertSeqs(t, drainAfterCancel(all), []uint64{1, 2, 3})
}

// Matches answers "who would receive this message" with stable, ID-ordered
// results, and excludes cancelled subscriptions.
func TestMatchesQuery(t *testing.T) {
	d := New()
	defer d.Close()
	s0 := mustSubscribe(t, d, Options{EntityPrefix: "user", Capacity: 1, OnFull: DropNewest})
	s1 := mustSubscribe(t, d, Options{EntityPrefix: "user", Attrs: []string{"name"}, Capacity: 1, OnFull: DropNewest})
	s2 := mustSubscribe(t, d, Options{EntityPrefix: "order", Capacity: 1, OnFull: DropNewest})

	assertMatchIDs(t, d.Matches("user/bob", "name"), []int{s0.ID(), s1.ID()})
	assertMatchIDs(t, d.Matches("user/bob", "email"), []int{s0.ID()})
	assertMatchIDs(t, d.Matches("superuser", "name"), nil)
	assertMatchIDs(t, d.Matches("order/7", "name"), []int{s2.ID()})

	// Stable across repeated calls.
	assertMatchIDs(t, d.Matches("user/bob", "name"), []int{s0.ID(), s1.ID()})

	// Cancelled subscriptions drop out of the result.
	s0.Cancel()
	assertMatchIDs(t, d.Matches("user/bob", "name"), []int{s1.ID()})
}

func assertMatchIDs(t *testing.T, got []*Subscription, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Matches returned %d subs, want %d (%v)", len(got), len(want), want)
	}
	for i, id := range want {
		if got[i].ID() != id {
			t.Fatalf("Matches[%d].ID() = %d, want %d", i, got[i].ID(), id)
		}
	}
}

// Subscribe rejects invalid capacity.
func TestSubscribeInvalidCapacity(t *testing.T) {
	d := New()
	defer d.Close()
	if _, err := d.Subscribe(Options{Capacity: 0}); err != ErrInvalidCapacity {
		t.Fatalf("Subscribe(Capacity:0) err = %v, want ErrInvalidCapacity", err)
	}
}
