package blame

import (
	"reflect"
	"testing"
)

func checkAlign(t *testing.T, name string, child, parent []string, want []int, why string) {
	t.Helper()
	got := alignLines(child, parent)
	t.Logf("用例 %s: child=%q parent=%q", name, child, parent)
	t.Logf("实际输出: %v", got)
	t.Logf("判定依据: %s", why)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alignLines(%q, %q) = %v, 期望 %v", child, parent, got, want)
	}
}

func TestAlignLines(t *testing.T) {
	checkAlign(t, "空内容", nil, []string{"a"}, []int{},
		"child 为空时没有可配对的行")
	checkAlign(t, "完全相同", []string{"a", "b"}, []string{"a", "b"}, []int{0, 1},
		"逐行相等，全部按序配对")
	checkAlign(t, "完全不相交", []string{"a"}, []string{"b"}, []int{-1},
		"无相等行，配对数为零")
	checkAlign(t, "插入一行", []string{"a", "x", "b"}, []string{"a", "b"}, []int{0, -1, 1},
		"新增行无配对，其余保持相对次序")
	checkAlign(t, "子侧重复取靠前", []string{"x", "x"}, []string{"x"}, []int{0, -1},
		"最多配一对；平局时靠前的 child 行优先配对")
	checkAlign(t, "父侧重复取靠前", []string{"x"}, []string{"x", "x"}, []int{0},
		"最多配一对；平局时靠前的 parent 行优先配对")
	checkAlign(t, "交叉取字典序最小", []string{"A", "B"}, []string{"B", "A"}, []int{1, -1},
		"最长配对为 1 对；候选 (0,1) 与 (1,0) 中取字典序最小的 (0,1)")
	checkAlign(t, "重复段保持次序", []string{"A", "B", "A"}, []string{"A", "A", "B"}, []int{0, 2, -1},
		"LCS 长度为 2；字典序最小的配对是 (0,0),(1,2)")
	checkAlign(t, "逐字节不等不配对", []string{"a "}, []string{"a"}, []int{-1},
		"行相等按逐字节比较，不做归一化，尾随空格视为不同行")
	checkAlign(t, "回车不归一化", []string{"a\r"}, []string{"a"}, []int{-1},
		"\\r 与无 \\r 逐字节不等，不配对")
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\n\nb\n", []string{"a", "", "b"}},
		{"\n", []string{""}},
	}
	for _, c := range cases {
		got := splitLines(c.in)
		t.Logf("输入 %q -> 实际输出 %q（依据：按 \\n 切分，末尾换行不产生额外空行）", c.in, got)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("splitLines(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
