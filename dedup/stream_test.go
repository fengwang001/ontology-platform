package dedup

import (
	"fmt"
	"testing"
)

// TestMillionRepeatsStayOneGroup feeds the same group a million times
// and asserts the group count stays one while the processed count
// grows: memory is bounded by groups, not by duplicates.
func TestMillionRepeatsStayOneGroup(t *testing.T) {
	const n = 1_000_000
	for _, mode := range []Mode{KeepFirst, KeepLast} {
		d := New([]string{"id"}, mode)
		for i := 0; i < n; i++ {
			d.Add(map[string]any{"id": int64(7), "payload": i})
		}
		if got := d.Groups(); got != 1 {
			t.Fatalf("mode=%d: got %d groups after %d rows, want 1", mode, got, n)
		}
		if got := d.Processed(); got != n {
			t.Fatalf("mode=%d: Processed = %d, want %d", mode, got, n)
		}
		snap := d.Snapshot()
		if len(snap) != 1 {
			t.Fatalf("mode=%d: snapshot has %d rows, want 1", mode, len(snap))
		}
		wantPayload := any(0)
		if mode == KeepLast {
			wantPayload = any(n - 1)
		}
		if snap[0]["payload"] != wantPayload {
			t.Fatalf("mode=%d: kept payload = %v, want %v",
				mode, snap[0]["payload"], wantPayload)
		}
	}
}

// TestGroupsFarBelowProcessed: many duplicates over few keys keeps the
// group count far below the processed count.
func TestGroupsFarBelowProcessed(t *testing.T) {
	d := New([]string{"id"}, KeepLast)
	const keys = 10
	const rounds = 100_000
	for i := 0; i < rounds; i++ {
		for k := 0; k < keys; k++ {
			d.Add(map[string]any{"id": int64(k), "payload": fmt.Sprintf("r%d", i)})
		}
	}
	if got := d.Processed(); got != keys*rounds {
		t.Fatalf("Processed = %d, want %d", got, keys*rounds)
	}
	if got := d.Groups(); got != keys {
		t.Fatalf("Groups = %d, want %d", got, keys)
	}
	if d.Groups()*100 > int(d.Processed()) {
		t.Fatalf("groups %d not far below processed %d", d.Groups(), d.Processed())
	}
}
