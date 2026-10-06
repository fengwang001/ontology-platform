package pathlock

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		illegal bool
	}{
		{"plain", "a/b/c", "a/b/c", false},
		{"dup slashes", "a//b///c", "a/b/c", false},
		{"leading trailing slash", "/a/b/", "a/b", false},
		{"dot segment", "a/./b/./c", "a/b/c", false},
		{"dotdot inside", "a/b/../c", "a/c", false},
		{"dotdot deeper", "a/b/c/../../d", "a/d", false},
		{"dotdot to root keeps rest", "a/../b", "b", false},
		{"only slash", "/", "", true},
		{"empty", "", "", true},
		{"only dots", "./.", "", true},
		{"escape root", "../a", "", true},
		{"escape after clean", "a/../../b", "", true},
		{"control byte", "a/\nb", "", true},
		{"nul byte", "a\x00b", "", true},
		{"del byte", "a\x7fb", "", true},
		{"dotdot at end lands file", "a/b/..", "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.input)
			t.Logf("input=%q actual=%q err=%v 判定依据=规范化规则", tc.input, got, err)
			if tc.illegal {
				if !errors.Is(err, ErrInvalidPath) {
					t.Fatalf("期望非法路径错误, 实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功, 实际 err=%v", err)
			}
			if got != tc.want {
				t.Fatalf("期望 %q, 实际 %q", tc.want, got)
			}
		})
	}
}

func TestNormalizeEquivalence(t *testing.T) {
	group := []string{
		"a//b/./c/../c/",
		"/a/b/c",
		"a/b/d/../c",
		"./a/b/c",
	}
	var canonical string
	for i, p := range group {
		got, err := Normalize(p)
		t.Logf("输入[%d]=%q 实际=%q err=%v 判定依据=同规范结果即同锁", i, p, got, err)
		if err != nil {
			t.Fatalf("不应非法: %v", err)
		}
		if i == 0 {
			canonical = got
		} else if got != canonical {
			t.Fatalf("%q -> %q != %q", p, got, canonical)
		}
	}
	if canonical != "a/b/c" {
		t.Fatalf("规范基准错误: %q", canonical)
	}
}
