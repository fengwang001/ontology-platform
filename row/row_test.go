package row

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
	name string
	a, b Row
	want int
	}{
		{"value orders first", Row{1, "z"}, Row{2, "a"}, -1},
		{"value desc", Row{2, "a"}, Row{1, "z"}, 1},
		{"tie breaks by id", Row{1, "a"}, Row{1, "b"}, -1},
		{"tie breaks by id desc", Row{1, "b"}, Row{1, "a"}, 1},
		{"equal key", Row{1, "a"}, Row{1, "a"}, 0},
		{"negative values", Row{-1, "a"}, Row{0, "a"}, -1},
		{"id byte order", Row{0, "a"}, Row{0, "ab"}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestKeyHelpers(t *testing.T) {
	r := Row{Value: 3.5, ID: "x"}
	k := r.Key()
	if !Equal(k, Key{3.5, "x"}) || Less(k, Key{3.5, "y"}) == false {
		t.Fatal("key helpers inconsistent")
	}
	if (Key{1, ""}).Valid() || !KeyValid(Key{1, "a"}) {
		t.Fatal("Valid misbehaved")
	}
}

// KeyValid is a package-level view of Key.Valid for table tests.
func KeyValid(k Key) bool { return k.Valid() }
