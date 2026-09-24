package replay

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/segment"
)

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		total   int // 0 表示空日志
		from    uint64
		to      uint64
		wantN   int
		wantErr error
	}{
		{"empty log", 0, 0, 10, 0, nil},
		{"single event", 1, 0, 0, 1, nil},
		{"from greater than to", 10, 5, 3, 0, ErrInvalidRange},
		{"to beyond max", 10, 8, math.MaxUint64, 2, nil},
		{"empty payload events", 3, 0, 2, 3, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.total > 0 {
				payload := 0 // 空载荷
				if tc.name != "empty payload events" {
					payload = 5
				}
				buildLog(t, dir, 100, 4, tc.total, payload)
			}
			evs, _, err := New(dir).Replay(tc.from, tc.to)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(evs) != tc.wantN {
				t.Fatalf("got %d events, want %d", len(evs), tc.wantN)
			}
		})
	}
}

func TestConcurrentAppendReplay(t *testing.T) {
	dir := t.TempDir()
	w, err := segment.NewWriter(dir, 500, 16)
	if err != nil {
		t.Fatal(err)
	}
	const total = 3000
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // 持续回放：每次都必须看到连续无缺口的一致前缀
		defer wg.Done()
		r := New(dir)
		for {
			select {
			case <-stop:
				return
			default:
			}
			evs, _, err := r.Replay(0, math.MaxUint64)
			if err != nil {
				t.Errorf("replay: %v", err)
				return
			}
			for i, e := range evs {
				if e.Seq != uint64(i) {
					t.Errorf("gap in prefix at %d: seq %d", i, e.Seq)
					return
				}
			}
		}
	}()
	for i := 0; i < total; i++ {
		if _, err := w.Append([]byte{byte(i), byte(i >> 8)}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	close(stop)
	wg.Wait()
	evs, _, err := New(dir).Replay(0, math.MaxUint64)
	if err != nil || len(evs) != total {
		t.Fatalf("final replay: %d events, err %v", len(evs), err)
	}
}
