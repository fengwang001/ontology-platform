package dict

import "testing"

// TestProbeCountO1 证明命中判定是哈希 O(1)：字典已含 m 个不同 value 后再查
// 一个新值，探测条目数恒为 1，不随 m（100…10000）线性增长。
// probes 为非导出字段，只在同包白盒测试里直接读取，不经任何导出接口。
func TestProbeCountO1(t *testing.T) {
	cases := []struct {
		name string
		m    int
	}{
		{"100", 100},
		{"1000", 1000},
		{"5000", 5000},
		{"10000", 10000},
	}
	var first int
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New(tc.m + 1)
			for j := 0; j < tc.m; j++ {
				d.Add(valueOf(j))
			}
			if _, ok := d.Lookup("definitely-absent"); ok {
				t.Fatalf("unexpected hit on absent value")
			}
			if i == 0 {
				first = d.probes
			}
			if d.probes != 1 {
				t.Fatalf("probes=%d, want constant 1 (hash lookup)", d.probes)
			}
			if d.probes != first {
				t.Fatalf("probes grew with m: got %d, baseline %d", d.probes, first)
			}
			if _, ok := d.Lookup(valueOf(tc.m - 1)); !ok || d.probes != 1 {
				t.Fatalf("hit probes=%d, want 1", d.probes)
			}
		})
	}
}

// TestCodeAssignment 钉住 code 按首次出现顺序分配、重置后回到 0。
func TestCodeAssignment(t *testing.T) {
	cases := []struct {
		k       int
		add     []string
		want    []int
		full    bool
		resetTo int
	}{
		{4, []string{"a", "b", "c"}, []int{0, 1, 2}, false, 0},
		{2, []string{"x", "y"}, []int{0, 1}, true, 0},
	}
	for _, tc := range cases {
		d := New(tc.k)
		for i, v := range tc.add {
			if got := d.Add(v); got != tc.want[i] {
				t.Fatalf("Add(%q)=%d, want %d", v, got, tc.want[i])
			}
		}
		if d.Full() != tc.full {
			t.Fatalf("Full=%v, want %v", d.Full(), tc.full)
		}
		d.Reset()
		if d.Len() != 0 {
			t.Fatalf("after reset Len=%d, want 0", d.Len())
		}
		if got := d.Add("z"); got != tc.resetTo {
			t.Fatalf("after reset first code=%d, want %d", got, tc.resetTo)
		}
	}
}

func valueOf(j int) string {
	// 固定宽度，便于人工核对；纯本地辅助。
	const digits = "0123456789abcdef"
	b := make([]byte, 8)
	for p := 7; p >= 0; p-- {
		b[p] = digits[j%16]
		j /= 16
	}
	return "v" + string(b)
}
