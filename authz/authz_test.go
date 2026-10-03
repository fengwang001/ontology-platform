package authz

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name   string
		in     []string
		expect []string
	}{
		{"空列表一个都看不见", nil, nil},
		{"去重", []string{"a", "a"}, []string{"a"}},
		{"空串全可见优先", []string{"a", ""}, []string{""}},
		{"排序输出", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"被更短前缀覆盖", []string{"ab", "a", "abc", "d"}, []string{"a", "d"}},
		{"前后缀不覆盖", []string{"ab", "ac", "b"}, []string{"ab", "ac", "b"}},
		{"多前缀交叉归一化", []string{"x/y", "x", "z/z", "z"}, []string{"x", "z"}},
		{"含零字节按字节序", []string{"b", "a\x00"}, []string{"a\x00", "b"}},
		{"零字节键被前缀覆盖", []string{"a\x00", "a"}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("Normalize(%q) = %q, 期望 %q", tc.in, got, tc.expect)
			}
			t.Logf("输入=%q 输出=%q 判定=去重并保留最短覆盖前缀后排序", tc.in, got)
		})
	}
}
