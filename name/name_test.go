package name

import (
	"errors"
	"reflect"
	"testing"
)

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", true},               // 空串合法
		{"a/b/c", true},          // 路径分隔符按普通字符
		{"\x00tmp0", true},       // 临时名形态合法
		{"名字", true},             // 多字节 UTF-8
		{"\xff\xfe", false},      // 非法 UTF-8
		{"a\xed\xa0\x80", false}, // 代理区非法
	}
	for _, c := range cases {
		if got := Valid(c.name); got != c.want {
			t.Errorf("Valid(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNamespace(t *testing.T) {
	n := New("a", "b", "")
	if !n.Has("") || !n.Has("a") || n.Has("z") || n.Len() != 3 {
		t.Fatalf("初始集合不对: %v", n.Snapshot())
	}
	ops := []struct {
		old, new string
		want     error
	}{
		{"a", "b", ErrExist},    // 目标已存在
		{"z", "y", ErrNotExist}, // 旧名不存在
		{"a", "a/b", nil},       // 含分隔符的普通改名
		{"", "empty", nil},      // 空串改名
	}
	for _, op := range ops {
		if err := n.Rename(op.old, op.new); !errors.Is(err, op.want) {
			t.Errorf("Rename(%q,%q) err=%v, want %v", op.old, op.new, err, op.want)
		}
	}
	want := []string{"a/b", "b", "empty"}
	if got := n.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %v, want %v", got, want)
	}
}
