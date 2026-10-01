package sepkey

import (
	"bytes"
	"errors"
	"testing"
)

func mustAdd(t *testing.T, b *Builder, last, next []byte) {
	t.Helper()
	if err := b.AddBlock(last, next); err != nil {
		t.Fatalf("AddBlock(%q, %q) 被拒绝: %v", last, next, err)
	}
}

func mustFinish(t *testing.T, b *Builder, last []byte) {
	t.Helper()
	if err := b.Finish(last); err != nil {
		t.Fatalf("Finish(%q) 被拒绝: %v", last, err)
	}
}

func sepsOf(t *testing.T, b *Builder) [][]byte {
	t.Helper()
	seps := b.Seps()
	for i := 1; i < len(seps); i++ {
		if bytes.Compare(seps[i-1], seps[i]) >= 0 {
			t.Fatalf("分隔键未严格递增: seps[%d]=%q seps[%d]=%q", i-1, seps[i-1], i, seps[i])
		}
	}
	return seps
}

// 覆盖: "abcdefg"/"abzzz"→"abd"、b+1 恰等于 next[d]、last 为 next 前缀、
// b 为 0xFF、next 为 last 前缀(应被拒绝)。
func TestAddBlockSep(t *testing.T) {
	cases := []struct {
		name string
		last []byte
		next []byte
		sep  []byte
	}{
		{"公共前缀缩短", []byte("abcdefg"), []byte("abzzz"), []byte("abd")},
		{"b+1恰等于next[d]不缩短", []byte("abc"), []byte("abd"), []byte("abc")},
		{"b+1恰等于next[d]不缩短带后缀", []byte("ab\x63xy"), []byte("ab\x64\x00"), []byte("ab\x63xy")},
		{"last为next的前缀", []byte("ab"), []byte("abzzz"), []byte("ab")},
		{"b为0xFF不缩短", []byte("ab\xff\x01"), []byte("ac"), []byte("ab\xff\x01")},
		{"无公共前缀可缩短", []byte("abc"), []byte("x"), []byte("b")},
		{"单字节可缩短", []byte("a"), []byte("c"), []byte("b")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder
			mustAdd(t, &b, tc.last, tc.next)
			seps := sepsOf(t, &b)
			if len(seps) != 1 || !bytes.Equal(seps[0], tc.sep) {
				t.Fatalf("sep=%q, 期望 %q", seps, tc.sep)
			}
			if bytes.Compare(seps[0], tc.last) < 0 {
				t.Fatalf("违反 last <= sep: sep=%q last=%q", seps[0], tc.last)
			}
			if bytes.Compare(seps[0], tc.next) >= 0 {
				t.Fatalf("违反 sep < next: sep=%q next=%q", seps[0], tc.next)
			}
			// 与朴素实现对拍
			want, why := naiveSep(tc.last, tc.next)
			t.Logf("输入 last=%q next=%q 输出 sep=%q 判定依据: %s", tc.last, tc.next, want, why)
			if !bytes.Equal(seps[0], want) {
				t.Fatalf("与朴素实现不一致: got=%q want=%q", seps[0], want)
			}
		})
	}
}

// next 为 last 的前缀时 last > next，必须被拒绝。
func TestAddBlockNextIsPrefixOfLast(t *testing.T) {
	var b Builder
	err := b.AddBlock([]byte("abzzz"), []byte("ab"))
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("err=%v, 期望 ErrOutOfOrder", err)
	}
	if got := b.Seps(); len(got) != 0 {
		t.Fatalf("被拒绝的 AddBlock 改变了已登记内容: %q", got)
	}
}

// Finish: 全 0xFF 与前缀 0xFF。
func TestFinishSep(t *testing.T) {
	cases := []struct {
		name string
		last []byte
		sep  []byte
	}{
		{"全0xFF", []byte{0xFF, 0xFF, 0xFF}, []byte{0xFF, 0xFF, 0xFF}},
		{"前缀0xFF", []byte{0xFF, 0xFF, 'a', 'z'}, []byte{0xFF, 0xFF, 'b'}},
		{"普通", []byte("abc"), []byte("b")},
		{"尾部0xFF", []byte("a\xff"), []byte("b")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder
			mustFinish(t, &b, tc.last)
			seps := sepsOf(t, &b)
			if len(seps) != 1 || !bytes.Equal(seps[0], tc.sep) {
				t.Fatalf("sep=%q, 期望 %q", seps, tc.sep)
			}
			want, why := naiveFinishSep(tc.last)
			t.Logf("输入 last=%q 输出 sep=%q 判定依据: %s", tc.last, want, why)
			if !bytes.Equal(seps[0], want) {
				t.Fatalf("与朴素实现不一致: got=%q want=%q", seps[0], want)
			}
		})
	}
}
