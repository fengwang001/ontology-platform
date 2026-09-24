package bucket

import (
	"reflect"
	"testing"
)

func TestSetOperations(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"union dedup sorted", func(t *testing.T) {
			s := NewSet(2)
			s.Add(0, 1, 3)
			s.Add(0, 1, 1)
			s.Add(1, 1, 3)
			s.Add(1, 1, 7)
			got := s.Candidates([]uint64{1, 1})
			if want := []int{1, 3, 7}; !reflect.DeepEqual(got, want) {
				t.Fatalf("candidates=%v want %v", got, want)
			}
		}},
		{"distinct signatures do not collide", func(t *testing.T) {
			s := NewSet(1)
			s.Add(0, 2, 9)
			if got := s.Candidates([]uint64{3}); len(got) != 0 {
				t.Fatalf("expected empty, got %v", got)
			}
		}},
		{"drop missing removes dangling refs and prunes", func(t *testing.T) {
			s := NewSet(2)
			s.Add(0, 1, 0)
			s.Add(0, 1, 5) // dangling
			s.Add(1, 2, 5) // whole bucket dangling
			s.Add(1, 3, 0)
			valid := map[int]bool{0: true}
			n := s.DropMissing(valid)
			if n != 2 {
				t.Fatalf("removed=%d want 2", n)
			}
			for _, id := range s.AllIDs() {
				if !valid[id] {
					t.Fatalf("dangling id %d remains", id)
				}
			}
			if s.BucketCount() != 2 {
				t.Fatalf("buckets=%d want 2", s.BucketCount())
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}
