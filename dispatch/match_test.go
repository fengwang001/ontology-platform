package dispatch

import (
	"reflect"
	"testing"
)

// TestPrefixNotSubstring: prefix matching must not degrade into substring
// matching — "user" must not match "superuser".
func TestPrefixNotSubstring(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "u", EntityPrefix: "user", BufferSize: 4,
	})
	mustPublish(t, d, "superuser", "name", nil)
	if _, ok := tryRecv(s); ok {
		t.Fatal("prefix \"user\" must not match entity \"superuser\"")
	}
	mustPublish(t, d, "user:42", "name", nil)
	mustPublish(t, d, "userProfile", "name", nil)
	if got := seqsOf(recvN(t, s, 2)); !equalSeqs(got, []uint64{2, 3}) {
		t.Fatalf("got seqs %v, want [2 3]", got)
	}
}

// TestPropertyExact: property names match by exact equality only; an empty
// property set matches every property.
func TestPropertyExact(t *testing.T) {
	d := New()
	defer d.Close()
	named := mustSubscribe(t, d, SubscribeOptions{
		ID: "named", EntityPrefix: "e", Properties: []string{"name"}, BufferSize: 4,
	})
	all := mustSubscribe(t, d, SubscribeOptions{
		ID: "all", EntityPrefix: "e", BufferSize: 8,
	})
	mustPublish(t, d, "e1", "name", 1)  // both
	mustPublish(t, d, "e1", "Name", 2)  // case differs: only "all"
	mustPublish(t, d, "e1", "names", 3) // superset string: only "all"
	mustPublish(t, d, "e1", "age", 4)   // only "all"
	if got := seqsOf(recvN(t, named, 1)); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("named got %v, want [1]", got)
	}
	if got := seqsOf(recvN(t, all, 4)); !equalSeqs(got, []uint64{1, 2, 3, 4}) {
		t.Fatalf("all got %v, want [1 2 3 4]", got)
	}
}

// TestMatchQuery: Match answers "who would receive this message" with a
// stable, sorted result, and reflects cancellations.
func TestMatchQuery(t *testing.T) {
	d := New()
	defer d.Close()
	mustSubscribe(t, d, SubscribeOptions{ID: "b-sub", EntityPrefix: "user"})
	mustSubscribe(t, d, SubscribeOptions{
		ID: "a-sub", EntityPrefix: "user", Properties: []string{"name"},
	})
	c := mustSubscribe(t, d, SubscribeOptions{ID: "c-sub", EntityPrefix: "order"})

	got := d.Match("user:1", "name")
	want := []string{"a-sub", "b-sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Match(user:1, name) = %v, want %v", got, want)
	}
	if got := d.Match("user:1", "age"); !reflect.DeepEqual(got, []string{"b-sub"}) {
		t.Fatalf("Match(user:1, age) = %v, want [b-sub]", got)
	}
	if got := d.Match("superuser", "name"); len(got) != 0 {
		t.Fatalf("Match(superuser, name) = %v, want empty", got)
	}
	if got := d.Match("order:9", "anything"); !reflect.DeepEqual(got, []string{"c-sub"}) {
		t.Fatalf("Match(order:9, anything) = %v, want [c-sub]", got)
	}
	c.Cancel()
	if got := d.Match("order:9", "anything"); len(got) != 0 {
		t.Fatalf("Match after cancel = %v, want empty", got)
	}
}

// TestEmptyPrefixMatchesAll: an empty entity prefix matches every entity.
func TestEmptyPrefixMatchesAll(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{ID: "s", BufferSize: 4})
	mustPublish(t, d, "anything", "p", 1)
	mustPublish(t, d, "superuser", "p", 2)
	if got := seqsOf(recvN(t, s, 2)); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got %v, want [1 2]", got)
	}
}
