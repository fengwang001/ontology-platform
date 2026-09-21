package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func makeRows(groups, rowsPer int) []map[string]any {
	rows := make([]map[string]any, 0, groups*rowsPer)
	for g := 0; g < groups; g++ {
		for i := 0; i < rowsPer; i++ {
			rows = append(rows, map[string]any{
				"g":  fmt.Sprintf("g%03d", g),
				"s":  float64((i*31 + g) % 97),
				"t":  fmt.Sprintf("t%02d", i%7),
				"id": g*rowsPer + i,
			})
		}
	}
	return rows
}

// TestConcurrentAddMatchesSerial feeds the same rows concurrently and
// serially and requires element-wise identical snapshots. While Adds are
// in flight, a watcher asserts no group ever holds more than N rows.
func TestConcurrentAddMatchesSerial(t *testing.T) {
	const (
		groups  = 20
		rowsPer = 500
		n       = 7
		workers = 8
	)
	rows := makeRows(groups, rowsPer)

	serial, err := New(testConfig(n))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		serial.Add(r)
	}
	want := serial.Snapshot()

	conc, err := New(testConfig(n))
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, gs := range conc.Snapshot() {
				if len(gs.Rows) > n {
					t.Errorf("group %v holds %d rows > N=%d", gs.Group, len(gs.Rows), n)
				}
			}
		}
	}()

	var wg sync.WaitGroup
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
				conc.Add(r)
			}
		}(rows[lo:hi])
	}
	wg.Wait()
	close(stop)
	watchWG.Wait()

	if got := conc.Processed(); got != int64(len(rows)) {
		t.Fatalf("lost or double-counted rows: want %d, got %d", len(rows), got)
	}
	if got := conc.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatal("concurrent result differs from serial result")
	}
}
