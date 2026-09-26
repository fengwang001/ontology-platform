package stream

import (
	"errors"
	"testing"

	"ontology/enc"
)

// 混合 1..4 字节 rune 造一段缓冲，返回 (字节, rune 序列)。
func mixBuf(m int) ([]byte, []rune) {
	fam := []rune{0x41, 0xA2, 0x20AC, 0x1F600}
	buf := []byte{}
	rs := make([]rune, 0, m)
	for i := 0; i < m; i++ {
		r := fam[i%len(fam)]
		b, _ := enc.EncodeRune(r)
		buf = append(buf, b...)
		rs = append(rs, r)
	}
	return buf, rs
}

// 单趟线性：总检查字节数 == 缓冲长度，额外回看恒为 0，与 m 无关。
func TestSinglePass(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		buf, want := mixBuf(m)
		got, err := DecodeAll(buf)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if string(got) != string(want) {
			t.Fatalf("m=%d: runes mismatch", m)
		}
		if c := stats.checked.Load(); c != int64(len(buf)) {
			t.Fatalf("m=%d: checked=%d, want %d (每字节恰好读一次)", m, c, len(buf))
		}
		if lb := stats.lookback.Load(); lb != 0 {
			t.Fatalf("m=%d: lookback=%d, want 0", m, lb)
		}
	}
}

// 失败不留痕：被拒的 Next 不推进游标，读取器不损坏。
func TestCursorPinnedOnError(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
		err  error
	}{
		{"invalid-lead", []byte{0x80}, enc.ErrInvalidLead},
		{"overlong", []byte{0xC0, 0xAF}, enc.ErrOverlong},
		{"surrogate", []byte{0xED, 0xA0, 0x80}, enc.ErrSurrogate},
		{"out-of-range", []byte{0xF4, 0x90, 0x80, 0x80}, enc.ErrOutOfRange},
		{"truncated", []byte{0xE2, 0x82}, enc.ErrTruncated},
		{"bad-continuation", []byte{0xE2, 0x28, 0xAC}, enc.ErrBadContinuation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(append([]byte{0x41}, tc.buf...)) // 先读走一个合法 rune
			if cp, err := r.Next(); err != nil || cp != 0x41 || r.Pos() != 1 {
				t.Fatalf("setup: cp=%U pos=%d err=%v", cp, r.Pos(), err)
			}
			p := r.Pos()
			for i := 0; i < 3; i++ { // 反复拒，游标纹丝不动
				if _, err := r.Next(); !errors.Is(err, tc.err) {
					t.Fatalf("want %v, got %v", tc.err, err)
				}
				if r.Pos() != p {
					t.Fatalf("cursor moved: %d -> %d", p, r.Pos())
				}
			}
			r.Reset([]byte{0x42}) // 读取器未损坏，仍可正常读
			if cp, err := r.Next(); err != nil || cp != 0x42 {
				t.Fatalf("after reset: cp=%U err=%v", cp, err)
			}
		})
	}
}

// DecodeAll：合法整段解出；任一非法即整体 (nil, error)。
func TestDecodeAll(t *testing.T) {
	buf, want := mixBuf(50)
	got, err := DecodeAll(buf)
	if err != nil || string(got) != string(want) {
		t.Fatalf("valid: err=%v match=%v", err, string(got) == string(want))
	}
	if rs, err := DecodeAll(nil); err != nil || len(rs) != 0 {
		t.Fatalf("empty: rs=%v err=%v", rs, err)
	}
	bad := append(append([]byte{}, buf...), 0xC0, 0xAF) // 尾部非法
	if rs, err := DecodeAll(bad); err == nil || rs != nil {
		t.Fatalf("bad tail: rs=%v err=%v, want (nil, error)", rs, err)
	}
}
