package ver

import "testing"

func TestPutAsOf(t *testing.T) {
	var s Versions
	put := func(vf int64, val string, tomb bool) {
		s.Put(Version{ValidFrom: vf, Value: val, Tombstone: tomb})
	}
	put(10, "A", false)
	put(20, "B", false)
	put(20, "B2", false) // same ValidFrom overwrites
	put(30, "", true)    // tombstone
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (overwrite must not append)", s.Len())
	}
	cases := []struct {
		ts    int64
		val   string
		found bool
		tomb  bool
	}{
		{9, "", false, false},   // before the first version
		{10, "A", true, false},  // ValidFrom == ts: left-closed interval
		{19, "A", true, false},  // last instant of A's interval
		{20, "B2", true, false}, // overwritten value wins
		{29, "B2", true, false},
		{30, "", true, true},  // tombstone is a lookup hit, not skipped
		{100, "", true, true}, // tombstone interval extends to +inf
	}
	for _, c := range cases {
		v, ok := s.AsOf(c.ts)
		if ok != c.found || v.Value != c.val || v.Tombstone != c.tomb {
			t.Errorf("AsOf(%d) = (%+v, %v), want value=%q found=%v tomb=%v",
				c.ts, v, ok, c.val, c.found, c.tomb)
		}
	}
}

// TestProbeLogBound proves AS OF is a binary search: for m versions the
// unexported probe counter must stay within ceil(log2(m+1)) + 3.
func TestProbeLogBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var s Versions
		for i := 0; i < m; i++ {
			s.Put(Version{ValidFrom: int64(i)})
		}
		tss := []int64{-1, 0, int64(m) / 2, int64(m - 1), int64(m)}
		for _, ts := range tss {
			s.AsOf(ts)
			if s.probes > probeBound(m) {
				t.Errorf("m=%d ts=%d: probes=%d exceeds bound %d", m, ts, s.probes, probeBound(m))
			}
		}
	}
}
