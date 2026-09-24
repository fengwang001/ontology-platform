package hist

import "testing"

func TestProbeConstant(t *testing.T) {
	// m 取多档；每档给目标键写 m 个版本，并穿插两个噪声键的写入。
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		var target, n1, n2 Chain
		for i := 1; i <= m; i++ {
			target.Append(i, i) // 目标键的版本 Seq 即 i
			if i%2 == 0 {
				n1.Append(i, i) // 穿插其他键
			}
			if i%5 == 0 {
				n2.Append(i, i)
			}
		}
		target.probeRead(m) // 读目标键最新位点（probeRead 记录检查过的版本数）
		if got := target.probe; got > 2 {
			t.Fatalf("m=%d: probe=%d, want <= 2 (与 m 无关的小常数)", m, got)
		}
	}
}

func TestChainAt(t *testing.T) {
	cases := []struct {
		name string
		seqs []int
		s    int
		want int
		ok   bool
	}{
		{"empty", nil, 0, 0, false},
		{"before first", []int{1, 3, 5}, 0, 0, false},
		{"at first (equal visible)", []int{1, 3, 5}, 1, 1, true},
		{"between", []int{1, 3, 5}, 2, 1, true},
		{"at second (equal)", []int{1, 3, 5}, 3, 3, true},
		{"beyond latest converges", []int{1, 3, 5}, 99, 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Chain
			for _, q := range tc.seqs {
				c.Append(q, q)
			}
			v, ok := c.At(tc.s)
			if ok != tc.ok || (ok && v != tc.want) {
				t.Fatalf("At(%d)=(%v,%v), want (%d,%v)", tc.s, v, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestChainCompact(t *testing.T) {
	cases := []struct {
		name string
		seqs []int
		upto int
		s    int
		want int
		ok   bool
	}{
		{"latest<=upto becomes base, still readable above", []int{2}, 2, 3, 2, true},
		{"base retained when newer live exists", []int{1, 3}, 2, 2, 1, true},
		{"base retained, read newer", []int{1, 3}, 2, 3, 3, true},
		{"nothing <=upto, chain unchanged", []int{5, 7}, 2, 2, 0, false},
		{"older versions discarded below base seq", []int{1, 2, 3, 8}, 3, 3, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Chain
			for _, q := range tc.seqs {
				c.Append(q, q)
			}
			c.Compact(tc.upto)
			v, ok := c.At(tc.s)
			if ok != tc.ok || (ok && v != tc.want) {
				t.Fatalf("after Compact(%d) At(%d)=(%v,%v), want (%d,%v)", tc.upto, tc.s, v, ok, tc.want, tc.ok)
			}
		})
	}
}
