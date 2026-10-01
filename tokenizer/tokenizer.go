// Package tokenizer 提供带字符过滤的流式分词器：把 UTF-8 文本流经
// 折叠与删除后切成小写字母数字词元，并把词元起止映射回原文字节偏移。
package tokenizer

import (
	"errors"
	"sync"
	"unicode/utf8"
)

// 拒绝原因，调用方可用 errors.Is 区分。
var (
	// ErrClosed 表示在已关闭的分词器上执行 Feed 或重复 Close。
	ErrClosed = errors.New("tokenizer: already closed")
	// ErrInvalidEncoding 表示块与暂存尾部拼接后含有已能确定不是任何
	// 合法 UTF-8 编码前缀的字节序列（含过长编码、代理项、超过 U+10FFFF）。
	ErrInvalidEncoding = errors.New("tokenizer: invalid UTF-8 encoding")
	// ErrTruncated 表示 Close 时仍有暂存的不完整尾部（输入被截断）。
	ErrTruncated = errors.New("tokenizer: truncated UTF-8 input")
)

// MaxTokenBytes 是词元输出字节数上限；超过则不报告（仍占位置序号）。
const MaxTokenBytes = 64

// Token 是一个已报告的词元。
type Token struct {
	Text  string // 过滤折叠后的词元字节（[a-z0-9]）
	Pos   int    // 位置序号，从 0 起；被丢弃的词元会造成缺口
	Start int    // 首个词元字节所来自的原文字符的起始字节偏移
	End   int    // 开区间终点：末个词元字节所来自字符的结束偏移，吞并紧随的连续被删除字符
}

// Stats 是 Close 成功后的统计。
type Stats struct {
	Reported int // 已报告词元数（不含 Dropped）
	Dropped  int // 因超过 MaxTokenBytes 而未报告的词元数
	Consumed int // 已消耗的原文字节数
}

// Tokenizer 是并发安全的流式分词器。
type Tokenizer struct {
	mu       sync.Mutex
	closed   bool
	pending  []byte // 暂不完整、等待后续块补齐的 UTF-8 尾部
	offset   int    // 已消耗的原文字节数（不含 pending）
	inToken  bool
	tok      []byte // 当前词元已累积的输出字节（封顶 MaxTokenBytes+1）
	tokStart int    // 当前词元首个词元字节所来自字符的起始偏移
	tokEnd   int    // 暂定终点：末个词元字节所来自字符的结束偏移，吞并尾随被删除字符
	pos      int    // 下一个位置序号（含被丢弃词元）
	dropped  int
}

// New 返回一个新的分词器。
func New() *Tokenizer {
	return &Tokenizer{}
}

// Feed 喂入一个任意切分的字节块，返回本次完成的词元（按序号升序）。
// 已关闭时报 ErrClosed；块与暂存尾部拼接后含非法 UTF-8 时报
// ErrInvalidEncoding，整块拒绝且不改变任何状态与统计。
func (t *Tokenizer) Feed(chunk []byte) ([]Token, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, ErrClosed
	}
	buf := make([]byte, 0, len(t.pending)+len(chunk))
	buf = append(buf, t.pending...)
	buf = append(buf, chunk...)
	n, ok := scanUTF8(buf)
	if !ok {
		return nil, ErrInvalidEncoding
	}
	var out []Token
	off := t.offset
	for i := 0; i < n; {
		r, size := utf8.DecodeRune(buf[i:n])
		t.processRune(r, off, off+size, &out)
		off += size
		i += size
	}
	t.offset = off
	t.pending = append([]byte(nil), buf[n:]...)
	return out, nil
}

// Close 结束输入，返回流末尾的词元与统计。
// 重复 Close 报 ErrClosed；仍有暂存的不完整尾部时报 ErrTruncated，
// 此时不关闭，可继续 Feed 补齐后再 Close。
func (t *Tokenizer) Close() ([]Token, Stats, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, Stats{}, ErrClosed
	}
	if len(t.pending) > 0 {
		return nil, Stats{}, ErrTruncated
	}
	var out []Token
	t.flushToken(&out)
	t.closed = true
	return out, Stats{Reported: t.pos - t.dropped, Dropped: t.dropped, Consumed: t.offset}, nil
}

// processRune 处理一个完整 rune，其原文占据 [start, end)。
func (t *Tokenizer) processRune(r rune, start, end int, out *[]Token) {
	var tmp [8]byte
	folded := appendFolded(tmp[:0], r)
	if len(folded) == 0 {
		// 被删除的字符：既不是词元字节也不是分隔符，不打断词元，
		// 但被词元吞并，延伸其暂定终点。
		if t.inToken {
			t.tokEnd = end
		}
		return
	}
	for _, b := range folded {
		if isTokenByte(b) {
			if !t.inToken {
				t.inToken = true
				t.tok = t.tok[:0]
				t.tokStart = start
			}
			if len(t.tok) <= MaxTokenBytes {
				t.tok = append(t.tok, b)
			}
			t.tokEnd = end
		} else {
			t.flushToken(out)
		}
	}
}

// flushToken 结束当前词元：超长词元丢弃但占位置序号并计入 Dropped。
func (t *Tokenizer) flushToken(out *[]Token) {
	if !t.inToken {
		return
	}
	t.inToken = false
	pos := t.pos
	t.pos++
	if len(t.tok) > MaxTokenBytes {
		t.dropped++
		return
	}
	*out = append(*out, Token{
		Text:  string(t.tok), // 复制，不别名内部缓冲
		Pos:   pos,
		Start: t.tokStart,
		End:   t.tokEnd,
	})
}

func isTokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// appendFolded 把一个原文字符过滤成零或多个输出字节并追加到 dst。
func appendFolded(dst []byte, r rune) []byte {
	switch {
	case r >= 0x0300 && r <= 0x036F:
		return dst // 组合记号：删除
	case r == 'ß':
		return append(dst, 's', 's')
	case r == 'æ' || r == 'Æ':
		return append(dst, 'a', 'e')
	case r == 'œ' || r == 'Œ':
		return append(dst, 'o', 'e')
	case r == '\uFB01':
		return append(dst, 'f', 'i')
	case r == '\uFB02':
		return append(dst, 'f', 'l')
	case r >= 'A' && r <= 'Z':
		return append(dst, byte(r+'a'-'A'))
	default:
		return utf8.AppendRune(dst, r)
	}
}

// scanUTF8 扫描 buf，返回构成完整合法 rune 序列的前缀字节数。
// ok 为 false 表示剩余字节已能确定不是任何合法 UTF-8 编码前缀
// （过长编码、代理项、超过 U+10FFFF、游离延续字节等）；
// ok 为 true 时剩余字节是合法但尚不完整的尾部。
func scanUTF8(buf []byte) (n int, ok bool) {
	for n < len(buf) {
		b0 := buf[n]
		if b0 < utf8.RuneSelf {
			n++
			continue
		}
		var size int
		var lo, hi byte // 第二字节的合法范围
		switch {
		case b0 >= 0xC2 && b0 <= 0xDF:
			size, lo, hi = 2, 0x80, 0xBF
		case b0 == 0xE0:
			size, lo, hi = 3, 0xA0, 0xBF
		case b0 >= 0xE1 && b0 <= 0xEC || b0 >= 0xEE && b0 <= 0xEF:
			size, lo, hi = 3, 0x80, 0xBF
		case b0 == 0xED:
			size, lo, hi = 3, 0x80, 0x9F // 排除代理项
		case b0 == 0xF0:
			size, lo, hi = 4, 0x90, 0xBF
		case b0 >= 0xF1 && b0 <= 0xF3:
			size, lo, hi = 4, 0x80, 0xBF
		case b0 == 0xF4:
			size, lo, hi = 4, 0x80, 0x8F // 不超过 U+10FFFF
		default:
			return n, false // 游离延续字节、0xC0/0xC1 过长编码、0xF5-0xFF
		}
		rest := buf[n:]
		check := len(rest)
		complete := true
		if check > size {
			check = size
		} else if check < size {
			complete = false
		}
		valid := true
		for i := 1; i < check; i++ {
			c := rest[i]
			l, h := byte(0x80), byte(0xBF)
			if i == 1 {
				l, h = lo, hi
			}
			if c < l || c > h {
				valid = false
				break
			}
		}
		if !valid {
			return n, false
		}
		if !complete {
			return n, true // 合法但尚不完整的尾部
		}
		n += size
	}
	return n, true
}
