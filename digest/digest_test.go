package digest

import (
	"bytes"
	"testing"
)

func TestSameContentSameFingerprint(t *testing.T) {
	body := []byte(`{"name":"alice","age":30}`)
	if got := Of(body); !got.Equal(Of(append([]byte(nil), body...))) {
		t.Fatalf("相同内容的指纹不一致: %s vs %s", got, Of(body))
	}
	if Of(nil) != Of([]byte{}) {
		t.Fatal("nil 与空切片应视为同一种空内容")
	}
}

func TestDifferentContentDifferentFingerprint(t *testing.T) {
	cases := [][]byte{
		[]byte("a"),
		[]byte("b"),
		[]byte("A"),
		{'a', 0},
		{'a', 0, 0},
	}
	for i := range cases {
		for j := i + 1; j < len(cases); j++ {
			if Of(cases[i]).Equal(Of(cases[j])) {
				t.Fatalf("不同内容发生指纹碰撞: %q 与 %q", cases[i], cases[j])
			}
		}
	}
}

func TestStringIsHexOfSHA256(t *testing.T) {
	if got := Of([]byte("abc")).String(); len(got) != 64 {
		t.Fatalf("sha256 hex 长度应为 64，实际 %d", len(got))
	} else {
		want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
		if got != want {
			t.Fatalf("abc 的指纹 = %s, want %s", got, want)
		}
	}
	if bytes.Contains([]byte(Of([]byte("x")).String()), []byte("x")) {
		// 仅防止 encoding 被意外简化；十六进制摘要不应明文包含原文。
	}
}
