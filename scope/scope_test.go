package scope

import "testing"

func TestCovers(t *testing.T) {
	cases := []struct {
		g, n string
		want bool
	}{
		{"orders", "orders", true},
		{"orders", "orders:read", true},
		{"orders", "orders:read:x", true},
		{"orders:read", "orders", false}, // 覆盖只向下
		{"orders:read", "orders:write", false},
		{"orders", "ordersx", false}, // 前缀但无 ":" 分隔
		{"orders", "ordersx:read", false},
		{"orders:read", "orders:readx", false},
		{"", ":read", true}, // g 为空串时的边界（gate 会拒绝空 need）
	}
	for _, tc := range cases {
		if got := Covers(tc.g, tc.n); got != tc.want {
			t.Errorf("Covers(%q,%q)=%v want %v", tc.g, tc.n, got, tc.want)
		}
	}
}

func TestFirstMissing(t *testing.T) {
	cases := []struct {
		name    string
		granted []string
		need    []string
		missing string
	}{
		{"orders covers children", []string{"orders"}, []string{"orders:read", "orders:read:x"}, ""},
		{"second missing", []string{"orders"}, []string{"orders:read", "billing:read"}, "billing:read"},
		{"upward not covered", []string{"orders:read"}, []string{"orders"}, "orders"},
		{"prefix no separator", []string{"orders"}, []string{"ordersx"}, "ordersx"},
	}
	for _, tc := range cases {
		if got := FirstMissing(tc.granted, tc.need); got != tc.missing {
			t.Errorf("%s: FirstMissing=%q want %q", tc.name, got, tc.missing)
		}
	}
}

func TestHighRisk(t *testing.T) {
	cases := []struct {
		need []string
		want bool
	}{
		{[]string{"orders:read"}, false},
		{[]string{"orders:write"}, true},
		{[]string{"admin"}, true},
		{[]string{"orders:read", "billing:admin"}, true},
		{[]string{"orders:write:x"}, false}, // 只看最后一段
		{[]string{"orders:x:write"}, true},
		{[]string{"orders"}, false},
		{[]string{"write:read"}, false}, // write 不是最后一段
	}
	for _, tc := range cases {
		if got := HighRisk(tc.need); got != tc.want {
			t.Errorf("HighRisk(%v)=%v want %v", tc.need, got, tc.want)
		}
	}
}
