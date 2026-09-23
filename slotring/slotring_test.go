package slotring

import "testing"

func TestSlot(t *testing.T) {
	cases := []struct {
		n    int
		seq  int64
		want int
	}{
		{4, 1, 0},
		{4, 4, 3},
		{4, 5, 0},
		{4, 8, 3},
		{1, 1, 0},
		{1, 99, 0},
		{3, 7, 0},
	}
	for _, tc := range cases {
		r := New(tc.n)
		if got := r.Slot(tc.seq); got != tc.want {
			t.Errorf("Slot(n=%d, seq=%d)=%d, want %d", tc.n, tc.seq, got, tc.want)
		}
	}
}

func TestPutGet(t *testing.T) {
	cases := []struct {
		name string
		n    int
		seqs []int64
	}{
		{"partial", 4, []int64{1, 2, 3}},
		{"exact-full", 4, []int64{1, 2, 3, 4}},
		{"wrapped", 4, []int64{1, 2, 3, 4, 5, 6}},
		{"capacity-one-wrap", 1, []int64{1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.n)
			for _, s := range tc.seqs {
				r.Put(s, s*10)
				if got, ok := r.Get(s); !ok || got != s*10 {
					t.Fatalf("Get(%d)=(%v,%v), want (%d,true)", s, got, ok, s*10)
				}
			}
			// 序号绕圈后，旧槽位承载新序号，旧序号必须读不出来（而非读到错内容）。
			stale := tc.seqs[len(tc.seqs)-1] - int64(tc.n)
			if stale >= 1 {
				if _, ok := r.Get(stale); ok {
					t.Fatalf("stale seq %d still reported present", stale)
				}
			}
			if r.Cap() != tc.n {
				t.Fatalf("Cap=%d, want %d", r.Cap(), tc.n)
			}
		})
	}
}

func TestNewInvalid(t *testing.T) {
	for _, n := range []int{0, -1, -10} {
		if New(n) != nil {
			t.Fatalf("New(%d) non-nil", n)
		}
	}
}
