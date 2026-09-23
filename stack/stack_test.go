package stack

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		maxDepth  int
		want      []string
		truncated bool
	}{
		{"empty", nil, 3, nil, false},
		{"single", []string{"A"}, 3, []string{"A"}, false},
		{"adjacent dedup", []string{"A", "A", "B"}, 3, []string{"A", "B"}, false},
		{"non adjacent same name kept", []string{"A", "F", "G", "F", "H"}, 8,
			[]string{"A", "F", "G", "F", "H"}, false},
		{"empty name frame legal", []string{"", "A", ""}, 8, []string{"", "A", ""}, false},
		{"depth equal limit", []string{"A", "B", "C"}, 3, []string{"A", "B", "C"}, false},
		{"depth over limit", []string{"A", "B", "C", "D"}, 3, []string{"A", "B", "C"}, true},
		{"zero limit keeps all", []string{"A", "B"}, 0, []string{"A", "B"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := make([]Frame, len(tc.in))
			for i, n := range tc.in {
				frames[i] = Frame{Name: n}
			}
			got := Normalize(frames, tc.maxDepth)
			if !eqNames(got.Names(), tc.want) || got.Truncated != tc.truncated {
				t.Fatalf("got %v trunc=%v, want %v trunc=%v",
					got.Names(), got.Truncated, tc.want, tc.truncated)
			}
			if tc.in != nil {
				if len(frames) != len(tc.in) {
					t.Fatalf("input slice mutated: %v", frames)
				}
			}
		})
	}
}

func TestHelpers(t *testing.T) {
	cases := []struct {
		names        []string
		empty, valid bool
	}{
		{nil, true, false},
		{[]string{}, true, false},
		{[]string{"A"}, false, true},
		{[]string{"A", "B"}, false, true},
	}
	for i, tc := range cases {
		s := FromNames(tc.names...)
		if s.Empty() != tc.empty || s.Valid() != tc.valid || s.Len() != len(tc.names) {
			t.Fatalf("case %d: empty=%v valid=%v len=%d", i, s.Empty(), s.Valid(), s.Len())
		}
		if !eqNames(s.Names(), tc.names) {
			t.Fatalf("case %d: names mismatch %v", i, s.Names())
		}
	}
	a, b := FromNames("A", "F"), FromNames("A", "F")
	if !a.Equal(b) || a.Equal(FromNames("A", "G")) || a.Equal(FromNames("A")) {
		t.Fatal("Equal behaves incorrectly")
	}
}

func eqNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
