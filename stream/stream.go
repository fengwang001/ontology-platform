// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 *Transcoder 不是并发安全的：同一时刻只能有一个 goroutine 调用它。
package stream

import (
	"errors"
	"fmt"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type From int

const (
	U8      From = iota // UTF-8
	U16LE               // 显式小端
	U16BE               // 显式大端
	U16Auto             // 仅按流首 BOM 判定，无 BOM 默认小端
)

type To int

const (
	ToU8 To = iota
	ToU16LE
	ToU16BE
)

// 四类可判定错误：非法字节 / 截断 / 超限 / 终态后写入。
var (
	ErrIllegalByte = errors.New("illegal byte unit")
	ErrTruncated   = errors.New("input truncated")
	ErrLimit       = errors.New("output limit exceeded")
	ErrClosed      = errors.New("write on terminal transcoder")
)

// ByteError 带非法单元在整个输入流中的起始偏移与长度。
type ByteError struct{ Off, Len int }

func (e *ByteError) Error() string { return fmt.Sprintf("%s at %d len %d", ErrIllegalByte, e.Off, e.Len) }
func (e *ByteError) Unwrap() error { return ErrIllegalByte }

// TruncError 是流结束时残留未完成前缀，与 ByteError 类型不同、可区分。
type TruncError struct{ Off, Len int }

func (e *TruncError) Error() string { return fmt.Sprintf("%s at %d len %d", ErrTruncated, e.Off, e.Len) }
func (e *TruncError) Unwrap() error { return ErrTruncated }

type Config struct {
	From    From
	To      To
	Strict  bool // 严格模式：首个非法单元立即终态
	KeepBOM bool // 流首 BOM 是否保留到输出
	Cap     int  // 输出字节上限；0 表示不限
}

type Stats struct {
	Valid      int // 已接受的合法标量数
	Bad        int // 非法单元数
	ValidBytes int // 合法标量占用的输入字节数
	BadBytes   int // 被非法单元吞掉的字节数
	BOMBytes   int // 流首 BOM 字节数
}

type Transcoder struct {
	cfg      Config
	out      []byte
	conf     int // 已确认边界的绝对输入偏移
	received int
	started  bool
	closed   bool
	frozen   error
	d8       u8.Decoder
	d16      *u16.Decoder
	auto     bool
	boDone   bool
	b0       int
	b0Off    int
	stats    Stats
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, b0: -1}
	if cfg.From != U8 {
		t.d16 = u16.NewDecoder()
		t.d16.SetBO(byte(0))
		if cfg.From == U16BE {
			t.d16.SetBO(1)
		}
		t.auto = cfg.From == U16Auto
	}
	return t
}

func (t *Transcoder) Output() []byte { return t.out }
func (t *Transcoder) Stats() Stats   { return t.stats }

// Examined 返回输入字节被检查的总次数（非导出计数的只读口）。
func (t *Transcoder) Examined() int {
	if t.cfg.From == U8 {
		return t.d8.Examined
	}
	return t.d16.Examined
}

type ev struct {
	bad     bool
	r       rune
	off, ln int
}

// Write 追加输入。返回的 n 恰好对应已输出标量覆盖的字节。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.frozen != nil {
		return 0, t.frozen
	}
	if t.closed {
		return 0, ErrClosed
	}
	abs0 := t.received
	limited := false
	emit := func(e ev) {
		if limited || t.frozen != nil {
			return
		}
		isBOM := !t.started && !e.bad && e.r == 0xFEFF
		t.started = true
		if e.bad {
			t.stats.Bad++
			t.stats.BadBytes += e.ln
			if t.cfg.Strict {
				t.frozen = &ByteError{Off: e.off, Len: e.ln}
				return
			}
			e.r = scalar.Replacement
		} else {
			t.stats.Valid++
			t.stats.ValidBytes += e.ln
		}
		if isBOM {
			t.stats.BOMBytes += e.ln
			t.stats.Valid--
			t.stats.ValidBytes -= e.ln
			if !t.cfg.KeepBOM {
				t.conf = e.off + e.ln
				return
			}
		}
		b := t.encode(e.r)
		if t.cfg.Cap > 0 && len(t.out)+len(b) > t.cfg.Cap {
			limited = true
			return
		}
		t.out = append(t.out, b...)
		t.conf = e.off + e.ln
	}
	cb := func(e ev) { emit(e) }
	if t.cfg.From == U8 {
		t.d8.Feed(p, abs0, func(e u8.Event) { cb(ev{e.Kind == u8.KBad, e.R, e.Off, e.Len}) })
	} else {
		for i, x := range p {
			t.feed16(x, abs0+i, cb)
		}
	}
	t.received += len(p)
	if limited {
		t.frozen = ErrLimit
		return t.conf - abs0, ErrLimit
	}
	if t.frozen != nil {
		return t.conf - abs0, t.frozen
	}
	held := t.pending()
	if held > len(p) {
		held = len(p)
	}
	return len(p) - held, nil
}

func (t *Transcoder) feed16(b byte, off int, cb func(ev)) {
	if t.auto && !t.boDone {
		if t.b0 < 0 {
			t.b0, t.b0Off = int(b), off
			return
		}
		if t.b0 == 0xFE && b == 0xFF {
			t.d16.SetBO(1)
		} else {
			t.d16.SetBO(0)
		}
		t.boDone = true
		t.d16.Feed([]byte{byte(t.b0), b}, t.b0Off, func(e u16.Event) { cb(ev{e.Kind == u16.KBad, e.R, e.Off, e.Len}) })
		return
	}
	t.d16.Feed([]byte{b}, off, func(e u16.Event) { cb(ev{e.Kind == u16.KBad, e.R, e.Off, e.Len}) })
}

func (t *Transcoder) pending() int {
	if t.cfg.From == U8 {
		return t.d8.Pending()
	}
	if t.auto && !t.boDone && t.b0 >= 0 {
		return 1
	}
	n := t.d16.Pending()
	return n
}

func (t *Transcoder) encode(r rune) []byte {
	switch t.cfg.To {
	case ToU8:
		return u8.EncodeRune(r)
	case ToU16BE:
		return u16.EncodeRune(r, 1)
	default:
		return u16.EncodeRune(r, 0)
	}
}

// Close 刷新残留前缀：替换模式补一个 FFFD，严格模式返回 *TruncError。
func (t *Transcoder) Close() error {
	if t.frozen != nil {
		return t.frozen
	}
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	if t.auto && !t.boDone && t.b0 >= 0 {
		t.flushEvent(t.b0Off, 1)
		t.b0 = -1
	}
	if t.cfg.From == U8 {
		t.d8.Flush(t.received, func(e u8.Event) { t.flushEvent(e.Off, e.Len) })
	} else {
		t.d16.Flush(t.received, func(e u16.Event) { t.flushEvent(e.Off, e.Len) })
	}
	return t.frozen
}

func (t *Transcoder) flushEvent(off, ln int) {
	if t.cfg.Strict {
		t.frozen = &TruncError{Off: off, Len: ln}
		return
	}
	t.stats.Bad++
	t.stats.BadBytes += ln
	b := t.encode(scalar.Replacement)
	if t.cfg.Cap > 0 && len(t.out)+len(b) > t.cfg.Cap {
		t.frozen = ErrLimit
		return
	}
	t.out = append(t.out, b...)
	t.conf = off + ln
}
