package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hb"
)

// Invariant 2, boundary rows: age==10 active, age==30 idle, age==31 dead.
func TestHBBoundaryTable(t *testing.T) {
	cases := []struct {
		last, now int64
		want      hb.State
	}{
		{0, 0, hb.Active}, {0, 10, hb.Active}, {0, 11, hb.Idle},
		{0, 30, hb.Idle}, {0, 31, hb.Dead}, {5, 1000, hb.Dead},
	}
	for _, c := range cases {
		d := api.New()
		if err := d.Heartbeat("s", c.last); err != nil {
			t.Fatal(err)
		}
		if got, err := d.Status("s", c.now); err != nil || got != c.want {
			t.Fatalf("last=%d now=%d: got %v,%v want %v", c.last, c.now, got, err, c.want)
		}
	}
	if got, err := api.New().Status("never", 0); err != nil || got != hb.Absent {
		t.Fatalf("absent stream: got %v,%v want absent", got, err)
	}
}

// Invariant 2: interleaved heartbeats and queries match the naive reference.
func TestNaiveReference(t *testing.T) {
	d := api.New()
	last := map[string]int64{}
	hbs := []struct {
		id string
		ts int64
	}{{"a", 0}, {"b", 3}, {"a", 12}, {"c", 20}, {"b", 3}, {"a", 12}}
	for _, h := range hbs {
		_ = d.Heartbeat(h.id, h.ts)
		if cur, ok := last[h.id]; !ok || h.ts >= cur {
			last[h.id] = h.ts
		}
	}
	for now := int64(20); now <= 80; now++ {
		for _, id := range []string{"a", "b", "c", "ghost"} {
			got, err := d.Status(id, now)
			if err != nil {
				t.Fatalf("id=%s now=%d: %v", id, now, err)
			}
			want := hb.Absent
			if ts, ok := last[id]; ok {
				want = hb.Classify(now - ts)
			}
			if got != want {
				t.Fatalf("id=%s now=%d: got %v want %v", id, now, got, want)
			}
		}
	}
}

// Fault injection: three distinguishable sentinels, state intact afterwards.
func TestAPISentinelErrors(t *testing.T) {
	d := api.New()
	_ = d.Heartbeat("s", 10)
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"hb empty id", d.Heartbeat("", 1), api.ErrEmptyID},
		{"hb negative ts", d.Heartbeat("s", -1), api.ErrNegativeTime},
		{"status empty id", func() error { _, e := d.Status("", 1); return e }(), api.ErrEmptyID},
		{"status negative now", func() error { _, e := d.Status("s", -1); return e }(), api.ErrNegativeTime},
		{"status clock back", func() error { _, e := d.Status("s", 9); return e }(), api.ErrClockBack},
		{"sweep negative now", func() error { _, e := d.Sweep(-1); return e }(), api.ErrNegativeTime},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, c.err, c.want)
		}
	}
	sentinels := []error{api.ErrEmptyID, api.ErrNegativeTime, api.ErrClockBack}
	for i := range sentinels {
		for j := range sentinels {
			if i != j && errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
	if got, _ := d.Status("s", 15); got != hb.Active {
		t.Fatalf("state changed after rejections: %v", got)
	}
}

// Concurrency: many goroutines read View of the same established streams;
// every result must be field-identical. No sleeps.
func TestConcurrentViewIdentical(t *testing.T) {
	d := api.New()
	for i := 0; i < 50; i++ {
		_ = d.Heartbeat(fmt.Sprintf("s%02d", i), int64(i))
	}
	base := d.View()
	start := make(chan struct{})
	var wg sync.WaitGroup
	mismatch := make(chan []api.ViewEntry, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 100; r++ {
				if v := d.View(); !reflect.DeepEqual(v, base) {
					mismatch <- v
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case v := <-mismatch:
		t.Fatalf("view diverged: %v", v)
	default:
	}
}

// The built-in self-check of the four invariants must pass.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
