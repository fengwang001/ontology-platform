package plan

import "testing"

func TestNext(t *testing.T) {
	p, err := New(10, []int{7, 3, 5}) // unordered input is fine
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		v, want int
	}{
		{1, 3},  // below all mandatory
		{3, 5},  // exactly a mandatory version: not repeated
		{4, 5},  // between mandatory versions
		{5, 7},  // exactly the next mandatory version
		{7, 10}, // last mandatory -> target
		{9, 10}, // above all mandatory -> target
	}
	for _, tc := range cases {
		if got := p.Next(tc.v); got != tc.want {
			t.Errorf("Next(%d) = %d, want %d", tc.v, got, tc.want)
		}
	}

	empty, err := New(2, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := empty.Next(1); got != 2 {
		t.Errorf("Next(1) with empty M = %d, want 2", got)
	}
	if empty.Target() != 2 {
		t.Errorf("Target() = %d, want 2", empty.Target())
	}
}

func TestNewInvalid(t *testing.T) {
	tooMany := make([]int, 65)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	cases := []struct {
		name string
		T    int
		M    []int
	}{
		{"target too small", 1, nil},
		{"target too big", 1_000_001, nil},
		{"zero mandatory", 10, []int{0}},
		{"mandatory >= T", 10, []int{10}},
		{"duplicate mandatory", 10, []int{3, 3}},
		{"too many mandatory", 100, tooMany},
	}
	for _, tc := range cases {
		if _, err := New(tc.T, tc.M); err != ErrInvalid {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	if _, err := New(1_000_000, []int{1, 999_999}); err != nil {
		t.Errorf("valid boundary: %v", err)
	}
}
