package scheduler

import (
	"reflect"
	"sync"
	"testing"

	"ontology/timer"
)

type recorder struct {
	mu  sync.Mutex
	log []int
}

func (r *recorder) add(id int) func() {
	return func() {
		r.mu.Lock()
		r.log = append(r.log, id)
		r.mu.Unlock()
	}
}

func (r *recorder) get() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.log...)
}

func newSched(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFireTiming(t *testing.T) {
	for _, d := range []int64{0, 1, 5, 63, 64, 100, 1000} {
		s := newSched(t, Config{})
		rec := &recorder{}
		if _, err := s.Add(d, rec.add(1)); err != nil {
			t.Fatal(err)
		}
		if d > 1 {
			if err := s.Advance(d - 1); err != nil {
				t.Fatal(err)
			}
		}
		if n := len(rec.get()); n != 0 {
			t.Fatalf("d=%d: fired %d times before deadline", d, n)
		}
		if err := s.Advance(1); err != nil {
			t.Fatal(err)
		}
		if n := len(rec.get()); n != 1 {
			t.Fatalf("d=%d: fired %d times at deadline, want 1", d, n)
		}
		if err := s.Advance(10); err != nil {
			t.Fatal(err)
		}
		if n := len(rec.get()); n != 1 {
			t.Fatalf("d=%d: fired %d times total, want exactly 1", d, n)
		}
		if err := s.Check(); err != nil {
			t.Fatalf("d=%d: %v", d, err)
		}
	}
}

func TestAdvanceEquivalence(t *testing.T) {
	delays := []int64{0, 1, 2, 5, 63, 64, 65, 100, 127, 200, 1000, 4095, 4096}
	run := func(chunked bool) []int {
		s := newSched(t, Config{})
		rec := &recorder{}
		for i, d := range delays {
			if _, err := s.Add(d, rec.add(i)); err != nil {
				t.Fatal(err)
			}
		}
		if chunked {
			for i := 0; i < 5000; i++ {
				if err := s.Advance(1); err != nil {
					t.Fatal(err)
				}
			}
		} else if err := s.Advance(5000); err != nil {
			t.Fatal(err)
		}
		return rec.get()
	}
	bulk, stepped := run(false), run(true)
	if !reflect.DeepEqual(bulk, stepped) {
		t.Fatalf("Advance(5000) != 5000x Advance(1):\n%v\n%v", bulk, stepped)
	}
	if len(bulk) != len(delays) {
		t.Fatalf("fired %d, want %d", len(bulk), len(delays))
	}
}

func TestSameTickOrder(t *testing.T) {
	delays := []int64{200, 5, 64, 5, 200, 64, 5}
	want := []int{1, 3, 6, 2, 5, 0, 4} // 按到期时刻分组，组内按注册先后
	run := func() []int {
		s := newSched(t, Config{})
		rec := &recorder{}
		for i, d := range delays {
			if _, err := s.Add(d, rec.add(i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Advance(300); err != nil {
			t.Fatal(err)
		}
		return rec.get()
	}
	first := run()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("order = %v, want %v", first, want)
	}
	if again := run(); !reflect.DeepEqual(first, again) {
		t.Fatalf("not deterministic: %v vs %v", first, again)
	}
}

func TestCancelInCallback(t *testing.T) {
	s := newSched(t, Config{})
	var b *timer.Timer
	var cancelErr error
	firedA, firedB := false, false
	if _, err := s.Add(5, func() { firedA = true; cancelErr = s.Cancel(b) }); err != nil {
		t.Fatal(err)
	}
	var err error
	b, err = s.Add(5, func() { firedB = true })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(5); err != nil {
		t.Fatal(err)
	}
	if !firedA || firedB {
		t.Fatalf("firedA=%v firedB=%v, want true/false", firedA, firedB)
	}
	if cancelErr != nil {
		t.Fatalf("cancel of pending-in-flight timer = %v, want nil", cancelErr)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if n := s.Pending(); n != 0 {
		t.Fatalf("Pending = %d, want 0", n)
	}
}
