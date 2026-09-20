package dedup

import (
	"fmt"
	"testing"
)

// TestKeepFirstVsKeepLast proves the two modes keep different rows when
// duplicates differ in non-dedup columns.
func TestKeepFirstVsKeepLast(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "payload": "first"},
		{"id": int64(2), "payload": "only"},
		{"id": int64(1), "payload": "last"},
	}
	first := New([]string{"id"}, KeepFirst)
	last := New([]string{"id"}, KeepLast)
	for _, r := range rows {
		first.Add(r)
		last.Add(r)
	}
	if first.Groups() != 2 || last.Groups() != 2 {
		t.Fatalf("groups: first=%d last=%d, want 2/2", first.Groups(), last.Groups())
	}
	fSnap := first.Snapshot()
	lSnap := last.Snapshot()
	if got := fSnap[0]["payload"]; got != "first" {
		t.Fatalf("KeepFirst payload = %v, want first", got)
	}
	if got := lSnap[0]["payload"]; got != "last" {
		t.Fatalf("KeepLast payload = %v, want last", got)
	}
	if fSnap[1]["payload"] != "only" || lSnap[1]["payload"] != "only" {
		t.Fatalf("singleton group must be identical in both modes")
	}
}

// TestOutputOrderIndependentOfArrival feeds the same rows in different
// orders and requires identical snapshot sequences in both modes.
func TestOutputOrderIndependentOfArrival(t *testing.T) {
	base := []map[string]any{
		{"id": int64(3), "s": "x", "payload": "p3a"},
		{"id": int64(1), "s": "b", "payload": "p1"},
		{"id": int64(3), "s": "a", "payload": "p3b"},
		{"id": int64(1), "s": "b", "payload": "p1dup"},
		{"id": int64(2), "s": "z", "payload": "p2"},
		{"id": int64(3), "s": "a", "payload": "p3c"},
	}
	shuffled := []map[string]any{base[4], base[2], base[0], base[5], base[3], base[1]}
	for _, mode := range []Mode{KeepFirst, KeepLast} {
		a := New([]string{"id", "s"}, mode)
		b := New([]string{"id", "s"}, mode)
		for _, r := range base {
			a.Add(r)
		}
		for _, r := range shuffled {
			b.Add(r)
		}
		sa, sb := a.Snapshot(), b.Snapshot()
		// Arrival order decides which row is kept (KeepLast differs
		// here), but never where a group lands in the output.
		if ka, kb := dedupKeys(sa), dedupKeys(sb); fmt.Sprint(ka) != fmt.Sprint(kb) {
			t.Fatalf("mode=%d key order differs:\n%v\n%v", mode, ka, kb)
		}
		// Ascending by id, then by s.
		wantKeys := []string{"1/b", "2/z", "3/a", "3/x"}
		if len(sa) != len(wantKeys) {
			t.Fatalf("mode=%d got %d rows, want %d", mode, len(sa), len(wantKeys))
		}
		for i, w := range wantKeys {
			if got := dedupKeys(sa)[i]; got != w {
				t.Fatalf("mode=%d row %d = %s, want %s", mode, i, got, w)
			}
		}
	}
}

func dedupKeys(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%v/%v", r["id"], r["s"])
	}
	return out
}

// TestSnapshotIsolation verifies snapshots do not alias internal state.
func TestSnapshotIsolation(t *testing.T) {
	d := New([]string{"id"}, KeepLast)
	d.Add(map[string]any{"id": int64(1), "payload": "a"})
	snap := d.Snapshot()
	snap[0]["payload"] = "mutated"
	snap[0]["extra"] = true
	d.Add(map[string]any{"id": int64(2), "payload": "b"})
	next := d.Snapshot()
	if len(next) != 2 {
		t.Fatalf("got %d rows, want 2", len(next))
	}
	if got := next[0]["payload"]; got != "a" {
		t.Fatalf("internal row mutated through snapshot: %v", got)
	}
	if _, ok := next[0]["extra"]; ok {
		t.Fatalf("snapshot mutation leaked into internal state")
	}
	// Mutating the input row after Add must not affect the deduper.
	row := map[string]any{"id": int64(9), "payload": "orig"}
	d.Add(row)
	row["payload"] = "changed"
	for _, r := range d.Snapshot() {
		if r["id"] == int64(9) && r["payload"] != "orig" {
			t.Fatalf("input mutation leaked into internal state")
		}
	}
}
