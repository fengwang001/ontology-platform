package dict

import "testing"

// TestLookupProbesConstant pins the O(1) claim: after inserting m distinct
// values, probing one new value must inspect a number of entries that does
// not grow with m. The counter is read directly (same package); no exported
// API can ever expose its value.
func TestLookupProbesConstant(t *testing.T) {
	cases := []struct {
		name string
		m    int
	}{
		{"100", 100},
		{"1000", 1000},
		{"5000", 5000},
		{"10000", 10000},
	}
	base := -1
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New(tc.m + 1)
			for i := 0; i < tc.m; i++ {
				d.Put(key(i))
			}
			if _, ok := d.Lookup(key(tc.m)); ok {
				t.Fatalf("Lookup of a never-seen value reported a hit at m=%d", tc.m)
			}
			got := d.probes
			if base == -1 {
				base = got
			}
			if got != base {
				t.Fatalf("m=%d probed %d entries, want %d: lookup is not O(1)",
					tc.m, got, base)
			}
		})
	}
}

// TestResetRestartCodes checks the allocation rule Reset must re-enable:
// after a clear the next inserted value gets code 0 again.
func TestResetRestartCodes(t *testing.T) {
	d := New(2)
	if c := d.Put("a"); c != 0 {
		t.Fatalf("first code = %d, want 0", c)
	}
	if c := d.Put("b"); c != 1 {
		t.Fatalf("second code = %d, want 1", c)
	}
	d.Reset()
	if d.Size() != 0 || d.Full() {
		t.Fatal("Reset did not empty the dictionary")
	}
	if c := d.Put("a"); c != 0 {
		t.Fatalf("post-reset code = %d, want 0", c)
	}
}
