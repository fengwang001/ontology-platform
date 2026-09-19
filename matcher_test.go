package ontology

import (
	"reflect"
	"testing"
)

func TestPrefixDoesNotDegradeToSubstring(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{Prefix: "user", BufferSize: 4})
	mustPublish(t, d, "superuser", "name", 1)
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("prefix user must not match superuser, got %+v", m)
	}
	mustPublish(t, d, "user1", "name", 2)
	m, ok := sub.TryReceive()
	if !ok || m.Entity != "user1" {
		t.Fatalf("expected message for user1, got %+v ok=%v", m, ok)
	}
}

func TestPropertyExactMatch(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix:     "e",
		Properties: []string{"name"},
		BufferSize: 4,
	})
	mustPublish(t, d, "e1", "name2", 1)
	mustPublish(t, d, "e1", "nam", 2)
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("fuzzy property match delivered %+v", m)
	}
	mustPublish(t, d, "e1", "name", 3)
	m, ok := sub.TryReceive()
	if !ok || m.Property != "name" {
		t.Fatalf("expected exact property match, got %+v ok=%v", m, ok)
	}
}

func TestEmptyPropertiesMatchAll(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{Prefix: "e", BufferSize: 4})
	mustPublish(t, d, "e1", "a", 1)
	mustPublish(t, d, "e1", "b", 2)
	if got := tryRecvSeqs(sub); len(got) != 2 {
		t.Fatalf("empty property set must match all, got %v", got)
	}
}

func TestMatchQueryStableAndSorted(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	mustSubscribe(t, d, SubscribeOptions{ID: "zeta", Prefix: "user", BufferSize: 1})
	mustSubscribe(t, d, SubscribeOptions{ID: "alpha", Prefix: "user", Properties: []string{"name"}, BufferSize: 1})
	beta := mustSubscribe(t, d, SubscribeOptions{ID: "beta", Prefix: "order", BufferSize: 1})
	got := d.Match("user1", "name")
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Match = %v, want %v", got, want)
	}
	if got := d.Match("user1", "email"); !reflect.DeepEqual(got, []string{"zeta"}) {
		t.Fatalf("Match with property filter = %v", got)
	}
	beta.Cancel()
	if got := d.Match("order1", "x"); len(got) != 0 {
		t.Fatalf("cancelled subscription still matched: %v", got)
	}
}
