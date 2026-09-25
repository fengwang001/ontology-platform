// Package api 规范 Huffman 编解码的对外接口。
package api

import (
	"errors"
	"fmt"

	"ontology/hcode"
	"ontology/htree"
)

// 四类可判定错误，互不相同，可 errors.Is 区分。
var (
	ErrInvalidFreq    = htree.ErrInvalidFreq
	ErrUnknownSymbol  = hcode.ErrUnknownSymbol
	ErrTruncated      = hcode.ErrTruncated
	ErrIllegalPadding = hcode.ErrIllegalPadding
)

// Codec 是只读码表，Encode/Decode 为纯函数，可并发调用。
type Codec struct {
	tab *hcode.Table
}

// New 由频次表构建 Codec；频次表非法时整体失败，不返回部分结果。
func New(freq map[byte]int) (*Codec, error) {
	lengths, err := htree.Lengths(freq)
	if err != nil {
		return nil, err
	}
	return &Codec{tab: hcode.New(lengths)}, nil
}

// Encode 编码消息；含未知符号时返回 ErrUnknownSymbol。
func (c *Codec) Encode(msg []byte) ([]byte, error) { return c.tab.Encode(msg) }

// Decode 从位流解出 n 个符号并校验填充。
func (c *Codec) Decode(b []byte, n int) ([]byte, error) { return c.tab.Decode(b, n) }

// CodeOf 返回符号的规范码字二进制串。
func (c *Codec) CodeOf(s byte) (string, bool) { return c.tab.CodeOf(s) }

// Stream 增量解码器：Feed 任意切块，Decode 结果与一次性 Decode 一致。
type Stream struct {
	c   *Codec
	buf []byte
}

// NewStream 创建增量解码器。
func (c *Codec) NewStream() *Stream { return &Stream{c: c} }

// Feed 追加一块字节流。
func (s *Stream) Feed(b []byte) { s.buf = append(s.buf, b...) }

// Decode 对已累积的字节流解出 n 个符号。
func (s *Stream) Decode(n int) ([]byte, error) { return s.c.Decode(s.buf, n) }

// SelfCheck 对内置频次与消息核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	c, err := New(freq)
	if err != nil {
		return err
	}
	c2, _ := New(freq) // 不变量 3：确定性
	for _, s := range []byte("ABCDE") {
		a, _ := c.CodeOf(s)
		b, _ := c2.CodeOf(s)
		if a != b {
			return fmt.Errorf("selfcheck: nondeterministic code for %q", s)
		}
		for _, t := range []byte("ABCDE") { // 前缀自由
			d, _ := c.CodeOf(t)
			if s != t && len(d) > len(a) && d[:len(a)] == a {
				return fmt.Errorf("selfcheck: %q is prefix of %q", a, d)
			}
		}
	}
	msgs := []string{"", "AABCCD", "ABCDEEDCBA", "AAAAA"}
	for _, m := range msgs { // 不变量 1：与朴素参照逐字节一致
		enc, err := c.Encode([]byte(m))
		if err != nil {
			return err
		}
		var naive []byte
		var cur byte
		nb := 0
		for i := 0; i < len(m); i++ {
			code, _ := c.CodeOf(m[i])
			for j := 0; j < len(code); j++ {
				cur = cur<<1 | (code[j] - '0')
				if nb++; nb == 8 {
					naive = append(naive, cur)
					cur, nb = 0, 0
				}
			}
		}
		if nb > 0 {
			naive = append(naive, cur<<(8-nb))
		}
		if string(naive) != string(enc) {
			return fmt.Errorf("selfcheck: encode mismatch for %q", m)
		}
		dec, err := c.Decode(enc, len(m)) // 往返
		if err != nil || string(dec) != m {
			return fmt.Errorf("selfcheck: roundtrip mismatch for %q", m)
		}
		st := c.NewStream() // 不变量 2：逐字节喂入
		for _, b := range enc {
			st.Feed([]byte{b})
		}
		dec2, err := st.Decode(len(m))
		if err != nil || string(dec2) != m {
			return fmt.Errorf("selfcheck: stream mismatch for %q", m)
		}
	}
	if _, err := New(map[byte]int{}); !errors.Is(err, ErrInvalidFreq) { // 不变量 4
		return fmt.Errorf("selfcheck: invalid freq not rejected")
	}
	if _, err := c.Encode([]byte("Z")); !errors.Is(err, ErrUnknownSymbol) {
		return fmt.Errorf("selfcheck: unknown symbol not rejected")
	}
	if _, err := c.Decode([]byte{0x25}, 6); !errors.Is(err, ErrTruncated) {
		return fmt.Errorf("selfcheck: truncation not rejected")
	}
	if _, err := c.Decode([]byte{0x25, 0xB9}, 6); !errors.Is(err, ErrIllegalPadding) {
		return fmt.Errorf("selfcheck: illegal padding not rejected")
	}
	if _, err := c.Encode([]byte("AABCCD")); err != nil { // 被拒后仍可用
		return fmt.Errorf("selfcheck: state corrupted after failures")
	}
	return nil
}
