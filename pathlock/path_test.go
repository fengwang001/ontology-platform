package pathlock

import "testing"

// TestNormalizeEquivalent 验证等价形态规范化后相同（即同一把锁）。
func TestNormalizeEquivalent(t *testing.T) {
	cases := []struct {
		inputs []string
		want   string
	}{
		{[]string{"a/b/c", "a//b///c", "/a/b/c/", "./a/./b/c", "a/x/../b/c"}, "a/b/c"},
		{[]string{"a", "./a", "a/", "//a//", "b/../a"}, "a"},
		{[]string{"a/b/../c/d", "a/c//d/", "/a/./c/d"}, "a/c/d"},
	}
	for _, c := range cases {
		for _, in := range c.inputs {
			got, err := NormalizePath(in)
			if err != nil {
				t.Fatalf("输入 %q 报错 %v，期望 %q", in, err, c.want)
			}
			t.Logf("输入=%q 实际输出=%q 判定依据=等价类期望 %q", in, got, c.want)
			if got != c.want {
				t.Errorf("输入 %q 得到 %q，期望 %q", in, got, c.want)
			}
		}
	}
}

// TestNormalizeInvalid 验证非法形态：空、逃出仓库根、不可打印字节。
func TestNormalizeInvalid(t *testing.T) {
	inputs := []string{
		"", "/", "//", ".", "./.", "/./",
		"..", "../a", "a/../..", "a/../../b", "a/b/../../../c",
		"a\x00b", "a\tb", "a\nb", "a\x1fb", "a\x7fb",
	}
	for _, in := range inputs {
		got, err := NormalizePath(in)
		pe, ok := err.(*Error)
		t.Logf("输入=%q 实际输出=(%q, %v) 判定依据=期望 ErrInvalidArgument", in, got, err)
		if !ok || pe.Code != ErrInvalidArgument {
			t.Errorf("输入 %q 期望 ErrInvalidArgument，得到 (%q, %v)", in, got, err)
		}
	}
}

// TestNormalizeValidKeepsUTF8 验证多字节 UTF-8 不被误判为不可打印。
func TestNormalizeValidKeepsUTF8(t *testing.T) {
	got, err := NormalizePath("模型/权重.bin")
	t.Logf("输入=%q 实际输出=(%q, %v) 判定依据=UTF-8 路径合法", "模型/权重.bin", got, err)
	if err != nil || got != "模型/权重.bin" {
		t.Errorf("得到 (%q, %v)", got, err)
	}
}
