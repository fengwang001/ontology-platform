package change

import "testing"

func TestTripleOrder(t *testing.T) {
	table := NewTable()
	key := []byte("k")

	if !table.Apply(Change{Origin: 1, Seq: 1, TS: 10, Key: key, Op: Put{Value: 1}}) {
		t.Fatal("first change must apply")
	}
	if table.Apply(Change{Origin: 2, Seq: 1, TS: 10, Key: key, Op: Put{Value: 2}}) != true {
		t.Fatal("same timestamp must be decided by larger origin")
	}
	if table.Apply(Change{Origin: 2, Seq: 2, TS: 10, Key: key, Op: Put{Value: 3}}) != true {
		t.Fatal("same origin and timestamp must be decided by larger seq")
	}
	entry, ok := table.Get(key)
	if !ok || entry.Value != 3 || entry.Triple.Seq != 2 {
		t.Fatalf("unexpected entry: %+v ok=%v", entry, ok)
	}
}

func TestDeleteTombstoneBeatsOlderPut(t *testing.T) {
	table := NewTable()
	key := []byte("k")

	if !table.Apply(Change{Origin: 1, Seq: 1, TS: 10, Key: key, Op: Del}) {
		t.Fatal("delete must apply")
	}
	if table.Apply(Change{Origin: 1, Seq: 2, TS: 9, Key: key, Op: Put{Value: 7}}) {
		t.Fatal("older put must not overwrite tombstone")
	}
	entry, ok := table.Get(key)
	if !ok || !entry.Delete || entry.Exists != true {
		t.Fatalf("expected tombstone, got %+v ok=%v", entry, ok)
	}
}
