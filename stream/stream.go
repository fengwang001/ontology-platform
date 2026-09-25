// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码。
// 单个 Transcoder 实例不是并发安全的。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Direction 是转换方向。
type Direction int

const (
	U8toU16LE Direction = iota
	U8toU16BE
	U16LEtoU8
	U16BEtoU8
	U16AutoToU8
	U8toU8
)

var (
	// ErrIllegal 遇到非法单元；错误为 *OffsetError。
	ErrIllegal = errors.New("stream: illegal unit")
	// ErrTruncated 输入被截断；错误为 *OffsetError。
	ErrTruncated = errors.New("stream: truncated input")
	// ErrLimit 输出超限（终态）；调用方从已消费偏移续传新实例。
	ErrLimit = errors.New("stream: output limit exceeded")
	// ErrClosed 终态后再次 Write。
	ErrClosed = errors.New("stream: transcoder in terminal state")
)

// OffsetError 携带单元在整个输入流中的起始偏移与字节长度。
type OffsetError struct {
	Err    error
	Offset int
	Length int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Stats 是字节守恒统计。
type Stats struct {
	Scalars int64 // 已接受的合法标量数
	Illegal int64 // 非法单元数
	BadBytes int64 // 非法单元吞掉的字节数
	BOMBytes int64 // 流首 BOM 字节数
	Consumed int64 // 已消费输入总字节数
	Checks   int64 // 字节被检查的总次数（非导出用途）
}

// Config 配置转码器。
type Config struct {
	Dir      Direction
	Strict   bool  // true=严格模式；false=替换模式
	EmitBOM  bool  // 流首 BOM 是否输出
	MaxOut   int   // 输出字节上限；0 表示不限
	Internal bool  // par 内部段：Close 不算 EOF 截断
}

type decoder interface {
	push(b byte, pos int) (unit, bool)
	pending() int
	closeUnit() (unit, bool)
}

type unit struct {
	r       rune
	start   int
	size    int
	illegal bool
	trunc   bool
	bom     bool
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg      Config
	dec      decoder
	u8d      *u8.Decoder
	u16d     *u16.Decoder
	out      []byte
	stats    Stats
	terminal bool
	terr     error
}

type u8Wrap struct{ d *u8.Decoder }

func (w u8Wrap) push(b byte, pos int) (unit, bool) {
	u, ok := w.d.Push(b, pos)
	return unit{u.R, u.Start, u.Size, u.Illegal, u.Trunc, false}, ok
}
func (w u8Wrap) pending() int { return w.d.Pending() }
func (w u8Wrap) closeUnit() (unit, bool) {
	u, ok := w.d.Close()
	return unit{u.R, u.Start, u.Size, u.Illegal, u.Trunc, false}, ok
}

type u16Wrap struct {
	d *u16.Decoder
	b byte
	p int
	h bool
}

func (w *u16Wrap) push(nb byte, pos int) (unit, bool) {
	// 先排空高代理失配时的 2 字节重放队列
	if w.h {
		b, p, ok := w.d.PopReplay()
		if ok {
			w.b, w.p, w.h = nb, pos, false
			u, done := w.d.Push(b, p)
			return w.mk(u, done)
		}
	}
	u, done := w.d.Push(nb, pos)
	return w.mk(u, done)
}

func (w *u16Wrap) mk(u u16.Unit, done bool) (unit, bool) {
	if done {
		return unit{u.R, u.Start, u.Size, u.Illegal, u.Trunc, u.BOM}, true
	}
	// Push 可能内部消化了重放的第一个字节而仍未完成：继续塞缓存的第二个
	if rb, rp, ok := w.d.PopReplay(); ok {
		w.h = true
		u2, done2 := w.d.Push(rb, rp)
		if done2 {
			return unit{u2.R, u2.Start, u2.Size, u2.Illegal, u2.Trunc, u2.BOM}, true
		}
	}
	return unit{}, false
}

func (w *u16Wrap) pending() int       { return w.d.Pending() }
func (w *u16Wrap) closeUnit() (unit, bool) {
	u, ok := w.d.Close(true)
	return unit{u.R, u.Start, u.Size, u.Illegal, u.Trunc, u.BOM}, ok
}

// New 按 cfg 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	switch cfg.Dir {
	case U16LEtoU8:
		t.u16d = u16.NewDecoder(u16.LE)
	case U16BEtoU8:
		t.u16d = u16.NewDecoder(u16.BE)
	case U16AutoToU8:
		t.u16d = u16.NewDecoder(u16.Auto)
	default:
		t.u8d = &u8.Decoder{}
	}
	if t.u8d != nil {
		t.dec = u8Wrap{t.u8d}
	} else {
		t.dec = &u16Wrap{d: t.u16d}
	}
	return t
}

func (t *Transcoder) emit(r rune) []byte {
	switch t.cfg.Dir {
	case U8toU16LE:
		return u16.Encode(r, u16.LE)
	case U8toU16BE:
		return u16.Encode(r, u16.BE)
	default:
		return u8.Encode(r)
	}
}

func (t *Transcoder) handle(u unit) (bool, error) {
	if u.illegal {
		t.stats.Illegal++
		t.stats.BadBytes += int64(u.size)
		if t.cfg.Strict {
			err := &OffsetError{ErrIllegal, u.start, u.size}
			if u.trunc {
				err = &OffsetError{ErrTruncated, u.start, u.size}
			}
			return false, err
		}
		u.r = scalar.RuneError
	} else if u.bom && !t.cfg.EmitBOM {
		return true, nil
	} else {
		t.stats.Scalars++
	}
	if u.bom {
		t.stats.BOMBytes += int64(u.size)
	}
	e := t.emit(u.r)
	if t.cfg.MaxOut > 0 && len(t.out)+len(e) > t.cfg.MaxOut {
		return false, ErrLimit
	}
	t.out = append(t.out, e...)
	return true, nil
}

// Write 喂入下一段字节。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal {
		return 0, t.terr
	}
	consumed := 0
	for _, b := range p { // 未使用 for range 语义解码；这里遍历 []byte
		t.stats.Checks++
		u, done := t.dec.push(b, int(t.stats.Consumed))
		t.stats.Consumed++
		if !done {
			continue
		}
		ok, err := t.handle(u)
		if err != nil {
			t.terminal, t.terr = true, err
			return consumed, err
		}
		consumed = int(t.stats.Consumed) - t.dec.pending()
		if !ok {
			continue
		}
	}
	return consumed, nil
}

// Close 在流结束时调用。
func (t *Transcoder) Close() error {
	if t.terminal {
		return t.terr
	}
	if t.cfg.Internal {
		t.terminal = true
		return nil
	}
	if u, ok := t.dec.closeUnit(); ok {
		_, err := t.handle(u)
		t.terminal = true
		if err != nil {
			t.terr = err
			return err
		}
	}
	t.terminal = true
	return nil
}

// Output 返回截至当前已输出的字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.stats }

// Pending 返回切分缓存中的字节数（硬上限 3）。
func (t *Transcoder) Pending() int { return t.dec.pending() }
