package dedup

import (
	"reflect"
	"testing"
)

func snapRows(groups []Group) []map[string]any {
	rows := make([]map[string]any, len(groups))
	for i, g := range groups {
		rows[i] = g.Row
	}
	return rows
}

func snapKeys(groups []Group) []string {
	keys := make([]string, len(groups))
	for i, g := range groups {
		keys[i] = g.Key
	}
	return keys
}

// The two keep modes must not leak into each other: with duplicate rows
// differing in a non-dedup column, KeepFirst and KeepLast keep
// different rows.
func TestKeepFirstVsKeepLast(t *testing.T) {
	mk := func(tag string, n int) map[string]any {
		return map[string]any{"id": int64(7), "tag": tag, "n": n}
	}
	first := New([]string{"id"}, KeepFirst)
	first.Add(mk("first", 1))
	first.Add(mk("middle", 2))
	first.Add(mk("last", 3))

	last := New([]string{"id"}, KeepLast)
	last.Add(mk("first", 1))
	last.Add(mk("middle", 2))
	last.Add(mk("last", 3))

	gotFirst := first.Snapshot()
	gotLast := last.Snapshot()
	if len(gotFirst) != 1 || len(gotLast) != 1 {
		t.Fatalf("want 1 group each, got %d and %d", len(gotFirst), len(gotLast))
	}
	if got := gotFirst[0].Row["tag"]; got != "first" {
		t.Fatalf("KeepFirst kept tag=%v, want first", got)
	}
	if got := gotLast[0].Row["tag"]; got != "last" {
		t.Fatalf("KeepLast kept tag=%v, want last", got)
	}
	if gotFirst[0].Row["n"] == gotLast[0].Row["n"] {
		t.Fatal("modes must keep different rows when non-dedup columns differ")
	}
}

// Output order must depend only on the dedup key, not arrival order:
// feeding the same rows in different orders yields identical snapshots,
// sorted ascending column by column.
func TestOutputOrderIndependentOfArrival(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(2), "b": "x"},
		{"a": int64(1), "b": "z"},
		{"a": int64(1), "b": "a"},
		{"a": int64(3), "b": "a"},
		{"a": int64(2), "b": "a"},
	}
	shuffled := []map[string]any{
		rows[4], rows[2], rows[0], rows[3], rows[1],
	}
	d1 := New([]string{"a", "b"}, KeepFirst)
	for _, r := range rows {
		d1.Add(r)
	}
	d2 := New([]string{"a", "b"}, KeepFirst)
	for _, r := range shuffled {
		d2.Add(r)
	}
	k1, k2 := snapKeys(d1.Snapshot()), snapKeys(d2.Snapshot())
	if !reflect.DeepEqual(k1, k2) {
		t.Fatalf("arrival order changed output: %v vs %v", k1, k2)
	}
	want := []map[string]any{
		{"a": int64(1), "b": "a"},
		{"a": int64(1), "b": "z"},
		{"a": int64(2), "b": "a"},
		{"a": int64(2), "b": "x"},
		{"a": int64(3), "b": "a"},
	}
	if got := snapRows(d1.Snapshot()); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot not sorted by dedup columns:\n got %v\nwant %v", got, want)
	}
}

// A snapshot must be fully detached from the internal state.
func TestSnapshotIsolation(t *testing.T) {
	d := New([]string{"id"}, KeepLast)
	d.Add(map[string]any{"id": int64(1), "v": "a"})
	d.Add(map[string]any{"id": int64(2), "v": "b"})

	snap := d.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 groups, got %d", len(snap))
	}
	// Mutating the snapshot must not affect the deduper.
	snap[0].Row["v"] = "corrupted"
	delete(snap[1].Row, "id")
	snap[0].Key = "corrupted"
	// Later Adds must not leak into the earlier snapshot.
	d.Add(map[string]any{"id": int64(1), "v": "replaced"})
	d.Add(map[string]any{"id": int64(3), "v": "c"})

	if got := snap[0].Row["v"]; got != "corrupted" {
		t.Fatalf("snapshot changed by later Add: v=%v", got)
	}
	if len(snap) != 2 {
		t.Fatalf("snapshot length changed: %d", len(snap))
	}
	fresh := d.Snapshot()
	if len(fresh) != 3 {
		t.Fatalf("want 3 groups after adds, got %d", len(fresh))
	}
	if got := fresh[0].Row["v"]; got != "replaced" {
		t.Fatalf("internal state corrupted by snapshot mutation: v=%v", got)
	}
	if _, ok := fresh[1].Row["id"]; !ok {
		t.Fatal("internal row lost a column after snapshot mutation")
	}
}

// Stats must reflect streaming Adds at any moment.
func TestGroupCountAndProcessed(t *testing.T) {
	d := New([]string{"id"}, KeepFirst)
	for i := 0; i < 10; i++ {
		d.Add(map[string]any{"id": int64(i % 3)})
	}
	if got := d.GroupCount(); got != 3 {
		t.Fatalf("GroupCount=%d, want 3", got)
	}
	if got := d.Processed(); got != 10 {
		t.Fatalf("Processed=%d, want 10", got)
	}
}
