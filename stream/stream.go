// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码。
// 单个 Transcoder 实例不是并发安全的。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// 编码标识。
const (
	UTF8 = iota
	UTF16LE
	UTF16BE
	UTF16 // 按开头 BOM 自动判定；无 BOM 按 LE
)

var (
	// ErrTruncated 用于 errors.Is 区分"输入被截断"。
	ErrTruncated = errors.New("stream: input truncated")
	// ErrLimit 表示输出越过上限。
	ErrLimit = errors.New("stream: output limit exceeded")
	// ErrTerminal 表示终态后再次写入。
	ErrTerminal = errors.New("stream: write after terminal error")
)

// IllegalError 携带非法单元在输入流中的起始偏移与长度。
type IllegalError struct{ Offset int64; Len int }

func (e *IllegalError) Error() string { return "stream: illegal byte sequence" }

// TruncatedError 是可与非法字节区分的截断错误，带起始偏移与长度。
type TruncatedError struct{ Offset int64; Len int }

func (e *TruncatedError) Error() string { return "stream: truncated input" }

// Is 支持 errors.Is(err, ErrTruncated)。
func (e *TruncatedError) Is(target error) bool { return target == ErrTruncated }

// Config 配置转码器；Limit<=0 表示不限输出。
type Config struct {
	From, To int
	Strict   bool
	KeepBOM  bool
	Limit    int
}

// Stats 是字节守恒统计。
type Stats struct {
	Scalars  int64
	Illegal  int64
	BadBytes int64
	BOMBytes int64
	Consumed int64
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg      Config
	out      []byte
	d8       *u8.Decoder
	d16      *u16.Decoder
	d16a     *u16.AutoDecoder
	big      bool
	seen     int64
	checks   int64
	scalars  int64
	illegal  int64
	badBytes int64
	bomBytes int64
	bomSeen  bool
	terminal bool
	err      error
	closed   bool
}

// New 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	switch cfg.From {
	case UTF8:
		t.d8 = &u8.Decoder{}
	case UTF16LE:
		t.d16 = u16.NewDecoder(false)
	case UTF16BE:
		t.d16, t.big = u16.NewDecoder(true), true
	default:
		t.d16a = u16.NewAutoDecoder()
	}
	return t
}

// Checks 返回字节被检查的总次数。
func (t *Transcoder) Checks() int64 { return t.checks }

// Stats 返回守恒统计。
func (t *Transcoder) Stats() Stats {
	return Stats{t.scalars, t.illegal, t.badBytes, t.bomBytes, t.seen}
}

// Output 返回已输出字节（调用方不应在继续 Write 后长期持有）。
func (t *Transcoder) Output() []byte { return t.out }

// Err 返回终态错误（无则 nil）。
func (t *Transcoder) Err() error { return t.err }

// Pending 返回底层解码器挂起的字节数（硬上限：UTF-8 3，UTF-16 3）。
func (t *Transcoder) Pending() int {
	switch {
	case t.d8 != nil:
		return t.d8.Pending()
	case t.d16 != nil:
		return t.d16.Pending()
	default:
		return t.d16a.Pending()
	}
}

// PendingBytes 返回挂起字节副本（供 par 承接前缀）。
func (t *Transcoder) PendingBytes() []byte {
	if t.d8 != nil {
		return t.d8.PendingBytes()
	}
	return nil // UTF-16 的承接在 par 内按段重算，不直接暴露
}

func (t *Transcoder) fit(r rune) bool {
	n := u8.Len(r)
	if t.cfg.To != UTF8 {
		n = u16.Len(r)
	}
	return t.cfg.Limit <= 0 || len(t.out)+n <= t.cfg.Limit
}

func (t *Transcoder) emit(r rune) {
	if t.cfg.To == UTF8 {
		t.out = u8.Encode(t.out, r)
		return
	}
	big := t.big
	if t.d16a != nil {
		big = t.d16aHasBig()
	}
	t.out = u16.Encode(t.out, r, big)
}

func (t *Transcoder) d16aHasBig() bool { return false }
