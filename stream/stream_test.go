package stream

import (
	"errors"
	"testing"

	"ontology/enc"
)

// 造一个含 m 个 rune 的缓冲，循环覆盖 1..4 字节长度（避开代理项区间）。
func makeBuf(m int) ([]byte, []rune) {
	ranges := [][2]rune{{0x00, 0x7F}, {0x80, 0x7FF}, {0x800, 0xD7FF}, {0x10000, 0x10FFFF}}
	var buf []byte
	var want []rune
	for i := 0; i < m; i++ {
		rg := ranges[i%4]
		r := rg[0] + rune(i)%(rg[1]-rg[0])
		b, err := enc.EncodeRune(r)
		if err != nil {
			panic(err)
		}
		buf = append(buf, b...)
		want = append(want, r)
	}
	return buf, want
}

func TestNextPosReset(t *testing.T) {
	buf, want := makeBuf(50)
	r := NewReader(buf)
	if r.Len() != len(buf) || r.Pos() != 0 {
		t.Fatalf("initial: Len=%d Pos=%d", r.Len(), r.Pos())
	}
	for i, w := range want {
		got, err := r.Next()
		if err != nil || got != w {
			t.Fatalf("Next #%d: got %U err %v, want %U", i, got, err, w)
		}
	}
	if r.Pos() != len(buf) {
		t.Fatalf("Pos=%d, want %d", r.Pos(), len(buf))
	}
	r.Reset([]byte{0x41})
	if r.Pos() != 0 || r.Len() != 1 {
		t.Fatalf("after Reset: Pos=%d Len=%d", r.Pos(), r.Len())
	}
	if rn, err := r.Next(); err != nil || rn != 'A' {
		t.Fatalf("Next after Reset: %U %v", rn, err)
	}
}

// 六类故障注入：哨兵互不相同、被拒后游标不变、读取器仍可正常使用。
func TestFaultInjectionsCursorStable(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"bad-lead-continuation", []byte{0x80}, enc.ErrBadLead},
		{"bad-lead-high", []byte{0xF8}, enc.ErrBadLead},
		{"overlong", []byte{0xC0, 0xAF}, enc.ErrOverlong},
		{"surrogate", []byte{0xED, 0xA0, 0x80}, enc.ErrSurrogate},
		{"out-of-range", []byte{0xF4, 0x90, 0x80, 0x80}, enc.ErrOutOfRange},
		{"truncated", []byte{0xE2, 0x82}, enc.ErrTruncated},
		{"bad-continuation", []byte{0xE2, 0x28, 0xAC}, enc.ErrBadContinuation},
	}
	for _, c := range cases {
		r := NewReader(append([]byte{0x41}, c.in...))
		if rn, err := r.Next(); err != nil || rn != 'A' { // 先读一个合法 rune
			t.Fatalf("%s: head Next: %U %v", c.name, rn, err)
		}
		pos := r.Pos()
		if _, err := r.Next(); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v, want %v", c.name, err, c.want)
		}
		if r.Pos() != pos { // 失败不留痕
			t.Fatalf("%s: Pos moved to %d", c.name, r.Pos())
		}
		r.Reset([]byte{0x42}) // 读取器未被污染，仍可正常使用
		if rn, err := r.Next(); err != nil || rn != 'B' {
			t.Fatalf("%s: after Reset: %U %v", c.name, rn, err)
		}
	}
}

// 单趟线性：总消耗字节数 == 缓冲长，额外回看计数恒为 0（与 m 无关）。
func TestSinglePassZeroLookback(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		buf, want := makeBuf(m)
		r := NewReader(buf)
		got, err := r.decodeAll()
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if len(got) != m {
			t.Fatalf("m=%d: decoded %d runes", m, len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("m=%d #%d: got %U want %U", m, i, got[i], want[i])
			}
		}
		if r.Pos() != len(buf) { // 每字节恰好读一次
			t.Fatalf("m=%d: consumed %d bytes, buf len %d", m, r.Pos(), len(buf))
		}
		if r.lookback != 0 { // 零额外回看
			t.Fatalf("m=%d: lookback=%d, want 0", m, r.lookback)
		}
	}
}

// 六个哨兵错误互不相同；编码端同样拒绝代理项与越界。
func TestSentinelsDistinct(t *testing.T) {
	all := []error{enc.ErrBadLead, enc.ErrOverlong, enc.ErrSurrogate, enc.ErrOutOfRange, enc.ErrTruncated, enc.ErrBadContinuation}
	seen := map[error]bool{}
	for _, e := range all {
		if seen[e] {
			t.Fatalf("duplicated sentinel: %v", e)
		}
		seen[e] = true
	}
	for _, r := range []rune{0xD800, 0xDFFF, -1, 0x110000} {
		if _, err := enc.EncodeRune(r); err == nil {
			t.Fatalf("EncodeRune(U+%04X) 未被拒绝", r)
		}
	}
}

// 整段解码：中间一个坏字节即整体失败，返回 (nil, error)。
func TestDecodeAllErrorWhole(t *testing.T) {
	buf, _ := makeBuf(20)
	bad := append([]byte{0x41, 0xFF}, buf...) // 坏字节在第二个位置
	got, err := DecodeAll(bad)
	if !errors.Is(err, enc.ErrBadLead) || got != nil {
		t.Fatalf("got %v runes, err %v", len(got), err)
	}
}
