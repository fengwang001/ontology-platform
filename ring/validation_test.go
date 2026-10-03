package ring

import (
	"errors"
	"testing"

	"ontology/change"
)

func TestRejectedWritesDoNotMutateState(t *testing.T) {
	r, err := New(3, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		site int
		key  []byte
		op   change.Op
		now  int64
		want error
	}{
		{"site out of range", 4, []byte("k"), change.Put{Value: 1}, 1, ErrInvalidArgument},
		{"empty key", 1, nil, change.Put{Value: 1}, 1, ErrInvalidArgument},
		{"long key", 1, make([]byte, 33), change.Put{Value: 1}, 1, ErrInvalidArgument},
		{"nil op", 1, []byte("k"), nil, 1, ErrInvalidArgument},
		{"negative now", 1, []byte("k"), change.Put{Value: 1}, -1, ErrInvalidArgument},
		{"future too large", 1, []byte("k"), change.Put{Value: 1}, 1_000_000_000_001, ErrInvalidArgument},
		{"not writable", 2, []byte("k"), change.Put{Value: 1}, 1, ErrNotWritable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Write(tc.site, tc.key, tc.op, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			for site := 1; site <= 3; site++ {
				for origin := 1; origin <= 3; origin++ {
					if floor, _ := r.Floor(site, origin); floor != 0 {
						t.Fatalf("floor[%d][%d] = %d after rejection", site, origin, floor)
					}
				}
			}
		})
	}

	if _, err := r.Write(1, []byte("k"), change.Put{Value: 1}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(1, []byte("k"), change.Put{Value: 2}, 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	entry, ok, err := r.Get(1, []byte("k"))
	if err != nil || !ok || entry.Value != 1 {
		t.Fatalf("rollback mutated value: entry=%+v ok=%v err=%v", entry, ok, err)
	}
}

func TestInvalidAdjacencyAndLinkErrors(t *testing.T) {
	r, err := New(4, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Deliver(1, 3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nonadjacent error = %v", err)
	}
	if _, err := r.Deliver(1, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("out-of-range error = %v", err)
	}
	if _, err := r.Pending(1, 3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("pending error = %v", err)
	}
	if err := r.SetLink(1, 4, false); err != nil {
		t.Fatal(err)
	}
	writeOK(t, r, 1, "k", change.Put{Value: 1}, 1)
	if _, err := r.Deliver(1, 4); !errors.Is(err, ErrDown) {
		t.Fatalf("down error = %v", err)
	}
	if err := r.SetLink(1, 4, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Deliver(2, 3); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty error = %v", err)
	}
}
