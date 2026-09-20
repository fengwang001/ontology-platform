package dedup

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// Repeating one key a million times must keep exactly one group;
// memory stays bounded because only one row is stored per group.
func TestMillionRepeatsGroupStaysOne(t *testing.T) {
	d := New([]string{"id"}, KeepLast)
	const n = 1_000_000
	for i := 0; i < n; i++ {
		d.Add(map[string]any{"id": int64(42), "payload": i})
	}
	if got := d.GroupCount(); got != 1 {
		t.Fatalf("GroupCount=%d, want 1", got)
	}
	if got := d.Processed(); got != n {
		t.Fatalf("Processed=%d, want %d", got, n)
	}
	if d.GroupCount()*1000 > int(d.Processed()) {
		t.Fatalf("groups %d not far below processed %d", d.GroupCount(), d.Processed())
	}
	if got := d.Snapshot()[0].Row["payload"]; got != n-1 {
		t.Fatalf("KeepLast payload=%v, want %d", got, n-1)
	}
}

func makeRows(n, keySpace int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		rows[i] = map[string]any{
			"k1": int64(i % keySpace),
			"k2": fmt.Sprintf("s%02d", i%7),
			"v":  i,
		}
	}
	return rows
}

func sortedKeys(groups []Group) []string {
	keys := snapKeys(groups)
	sort.Strings(keys)
	return keys
}

// Concurrent Adds must not lose rows or duplicate groups: the final
// group count and the set of dedup keys match a serial run of the
// same data. (KeepLast may keep a different row; keys must agree.)
func TestConcurrentAddMatchesSerial(t *testing.T) {
	rows := makeRows(40_000, 100)
	serial := New([]string{"k1", "k2"}, KeepLast)
	for _, r := range rows {
		serial.Add(r)
	}
	concurrent := New([]string{"k1", "k2"}, KeepLast)
	var wg sync.WaitGroup
	const workers = 8
	chunk := len(rows) / workers
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if w == workers-1 {
			hi = len(rows)
		}
		wg.Add(1)
		go func(part []map[string]any) {
			defer wg.Done()
			for _, r := range part {
				concurrent.Add(r)
			}
		}(rows[lo:hi])
	}
	wg.Wait()
	if got, want := concurrent.GroupCount(), serial.GroupCount(); got != want {
		t.Fatalf("GroupCount=%d, serial has %d", got, want)
	}
	if got, want := concurrent.Processed(), serial.Processed(); got != want {
		t.Fatalf("Processed=%d, serial has %d", got, want)
	}
	gotKeys := sortedKeys(concurrent.Snapshot())
	wantKeys := sortedKeys(serial.Snapshot())
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("dedup keys differ: concurrent=%d serial=%d", len(gotKeys), len(wantKeys))
	}
}

// Snapshots taken while Adds are in flight must never observe a
// half-updated state: keys are unique and sorted at all times.
func TestConcurrentSnapshotConsistency(t *testing.T) {
	rows := makeRows(20_000, 50)
	d := New([]string{"k1", "k2"}, KeepLast)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := d.Snapshot()
			for i := 1; i < len(snap); i++ {
				if snap[i-1].Key == snap[i].Key {
					t.Errorf("duplicate key in snapshot: %q", snap[i].Key)
				}
			}
			if len(snap) > d.GroupCount() {
				t.Errorf("snapshot larger than group count")
			}
		}
	}()

	var adders sync.WaitGroup
	const workers = 4
	chunk := len(rows) / workers
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if w == workers-1 {
			hi = len(rows)
		}
		adders.Add(1)
		go func(part []map[string]any) {
			defer adders.Done()
			for _, r := range part {
				d.Add(r)
			}
		}(rows[lo:hi])
	}
	adders.Wait()
	close(stop)
	wg.Wait()

	if got := d.GroupCount(); got != 50*7 {
		t.Fatalf("GroupCount=%d, want %d", got, 50*7)
	}
	if got := d.Processed(); got != int64(len(rows)) {
		t.Fatalf("Processed=%d, want %d", got, len(rows))
	}
}
