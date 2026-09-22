package scheduler

import "testing"

// TestAdvanceEquivalence pins invariant 2: any chunking of the same total
// advance yields the identical firing sequence.
func TestAdvanceEquivalence(t *testing.T) {
	chunkings := []struct {
		name  string
		steps []int64
	}{
		{"single 100", []int64{100}},
		{"hundred 1s", func() []int64 {
			s := make([]int64, 100)
			for i := range s {
				s[i] = 1
			}
			return s
		}()},
		{"uneven chunks", []int64{7, 33, 60}},
		{"halves", []int64{50, 50}},
	}
	var want []uint64
	for _, c := range chunkings {
		t.Run(c.name, func(t *testing.T) {
			s, r := newRecorderScheduler(Config{})
			for i := 0; i < 200; i++ {
				if _, err := s.Add(int64((i*37)%100)+1, nil); err != nil {
					t.Fatalf("Add: %v", err)
				}
			}
			for _, step := range c.steps {
				mustAdvance(t, s, step)
			}
			got := r.ids()
			if want == nil {
				want = got
				return
			}
			if len(got) != len(want) {
				t.Fatalf("fired %d, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("position %d: got id %d, want %d", i, got[i], want[i])
				}
			}
		})
	}
}

// TestSameTickOrder pins invariant 3: same-tick firings follow Add order,
// independent of the wheel level each timer happens to sit in.
func TestSameTickOrder(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, s *Scheduler) []uint64 // returns expected ID order
	}{
		{"same delay added together", func(t *testing.T, s *Scheduler) []uint64 {
			var ids []uint64
			for i := 0; i < 10; i++ {
				id, _ := s.Add(5, nil)
				ids = append(ids, id)
			}
			mustAdvance(t, s, 5)
			return ids
		}},
		{"same deadline from different levels", func(t *testing.T, s *Scheduler) []uint64 {
			a, _ := s.Add(10, nil) // sits in a higher level
			mustAdvance(t, s, 5)
			b, _ := s.Add(5, nil) // same deadline 10, lower level
			mustAdvance(t, s, 5)
			return []uint64{a, b}
		}},
		{"add order wins over arrival order", func(t *testing.T, s *Scheduler) []uint64 {
			x, _ := s.Add(3, nil)
			mustAdvance(t, s, 2)
			y, _ := s.Add(1, nil) // deadline 3, same tick as x
			mustAdvance(t, s, 1)
			return []uint64{x, y}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newRecorderScheduler(Config{})
			want := tt.run(t, s)
			got := r.ids()
			if len(got) != len(want) {
				t.Fatalf("fired %v, want order %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("fired %v, want order %v", got, want)
				}
			}
		})
	}
}
