// Package stream 实现带非法字节替换的流式转码。
//
// 单个 Transcoder 不要求并发安全。
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

type Direction int

const (
	U8ToU8 Direction = iota
	U8ToU16LE
	U8ToU16BE
	U16LEToU8
	U16BEToU8
)

type Config struct {
	Dir     Direction
	Strict  bool
	AutoBOM bool
	KeepBOM bool
	MaxOut  int64
}

type Stats struct {
	Scalars      int64
	Illegal      int64
	IllegalBytes int64
	BOMBytes     int64
}

type IllegalError struct{ Offset int64; Length int }

func (e *IllegalError) Error() string { return "illegal byte sequence" }

type TruncatedError struct{ Offset int64 }

func (e *TruncatedError) Error() string { return "truncated input" }

var (
	ErrLimit  = errors.New("output limit exceeded")
	ErrClosed = errors.New("transcoder closed")
)

type Transcoder struct {
	cfg    Config
	out    []byte
	st     Stats
	checks int64
	pos    int64
	fatal  error
	closed bool
	win    *Window

	// UTF-8 状态
	need, cls, part int
	cp              rune
	lead            byte
	start           int64
	// UTF-16 状态
	order        int
	haveHalf     bool
	half         byte
	haveHigh     bool
	high         rune
	hiStart       int64
	probe         int
	pb            [2]byte
	limPos        int64
}

type Window struct {
	Start, End int64
	Last       bool
}

func New(c Config) *Transcoder {
	t := &Transcoder{cfg: c, order: -1}
	if c.Dir == U16LEToU8 {
		t.order = u16.LE
	}
	if c.Dir == U16BEToU8 {
		t.order = u16.BE
	}
	return t
}

func (t *Transcoder) in16() bool { return t.cfg.Dir >= U16LEToU8 }

func (t *Transcoder) pending() int64 {
	if t.in16() {
		return int64(boolToInt(t.haveHalf) + 2*boolToInt(t.haveHigh))
	}
	return int64(t.part)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Write 喂入字节；返回的 n 为本次输入中已完全解决（已输出或已判定）的字节数，
// 残留在切分缓存中的字节不计入 n。出错后转码器进入终态。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	if t.fatal != nil {
		return 0, t.fatal
	}
	saved := t.pos - t.pending()
	for i := range p {
		abs := t.pos
		t.pos++
		if t.in16() {
			t.b16(p[i], abs)
		} else {
			t.b8(p[i], abs)
		}
		if t.fatal != nil {
			break
		}
	}
	if t.fatal == ErrLimit {
		return int(t.limPos - saved), t.fatal
	}
	return int(t.pos - t.pending() - saved), t.fatal
}

// Close 报告流结束：残留合法前缀在替换模式产生一个 U+FFFD，严格模式报截断。
func (t *Transcoder) Close() error {
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	if t.fatal != nil {
		return t.fatal
	}
	last := t.win == nil || t.win.Last
	if !last {
		return nil
	}
	if t.in16() {
		if t.probe == 1 {
			t.orphan(0, t.pos-1, 1)
		}
		if t.haveHalf {
			t.orphan(0, t.pos-1, 1)
		}
		if t.haveHigh {
			t.orphan(0, t.hiStart, 2)
		}
	} else if t.need > 0 {
		t.orphan(0, t.start, t.part)
	}
	return t.fatal
}

func (t *Transcoder) Output() []byte { return t.out }
func (t *Transcoder) Stats() Stats   { return t.st }
func (t *Transcoder) Checks() int64  { return t.checks }

// Pos 为下一个未解决字节在整个输入流中的绝对偏移（含缓存），用于上限续传。
func (t *Transcoder) Pos() int64 {
	if t.fatal == ErrLimit {
		return t.limPos
	}
	return t.pos - t.pending()
}

func (t *Transcoder) report(start int64) bool {
	return t.win == nil || (start >= t.win.Start && start < t.win.End)
}

// emit 处理一个完整单元：start 为其首字节绝对偏移；bom 为 BOM 字节数。
func (t *Transcoder) emit(r rune, legal, bom bool, start int64, n int) {
	if bom {
		if t.cfg.KeepBOM {
			if !t.put(0xFEFF) {
				return
			}
			t.st.Scalars++
		}
		t.st.BOMBytes += int64(n)
		return
	}
	if !t.report(start) {
		return
	}
	if legal {
		if !t.put(r) {
			return
		}
		t.st.Scalars++
		return
	}
	if t.cfg.Strict {
		t.fatal = &IllegalError{Offset: start, Length: n}
		return
	}
	if !t.put(0xFFFD) {
		return
	}
	t.st.Illegal++
	t.st.IllegalBytes += int64(n)
}

func (t *Transcoder) put(r rune) bool {
	var buf [4]byte
	var n int
	switch t.cfg.Dir {
	case U8ToU8:
		n = u8.Encode(r, buf[:])
	case U16LEToU8, U16BEToU8:
		n = u8.Encode(r, buf[:])
	default:
		order := u16.LE
		if t.cfg.Dir == U8ToU16BE {
			order = u16.BE
		}
		n = u16.Encode(r, order, buf[:])
	}
	if t.cfg.MaxOut > 0 && int64(len(t.out))+int64(n) > t.cfg.MaxOut {
		t.limPos = t.pos - t.pending()
		t.fatal = ErrLimit
		return false
	}
	t.out = append(t.out, buf[:n]...)
	return true
}

// orphan 在 EOF 或非法处终结一个不完整单元。
func (t *Transcoder) orphan(_ rune, start int64, n int) {
	if n > 0 {
		t.emit(0, false, false, start, n)
	}
}

func (t *Transcoder) b8(b byte, abs int64) {
	t.checks++
	if t.need == 0 {
		// 仅流开头识别 UTF-8 BOM（可跨切分）。
		if t.pos-t.pending() == 0 {
			switch t.part {
			case 0:
				if b == 0xEF {
					t.part = 1
					return
				}
			case 1:
				t.part = 0
				if b == 0xBB {
					t.part = 2
					return
				}
				if !u8.IsCont(b) {
					t.b8(b, abs)
					return
				}
				t.orphan(0, 0, 2)
				return
			case 2:
				t.part = 0
				if b == 0xBF {
					t.emit(0, false, true, 0, 3)
					return
				}
				t.orphan(0, 0, 3)
				if t.fatal == nil && !u8.IsCont(b) {
					t.b8(b, abs)
				}
				return
			}
		}
		cls := u8.Class(b)
		switch cls {
		case 0:
			t.emit(rune(b), true, false, abs, 1)
		case 1:
			t.emit(0, false, false, abs, 1)
		default:
			t.need, t.cls, t.part = cls-1, cls, 1
			t.lead, t.start = b, abs
			t.cp = rune(b & (0xFF >> uint(cls+1)))
		}
		return
	}
	t.checks++
	bad := !u8.IsCont(b)
	if t.part == 1 {
		if lo, hi, ok := u8.Second(t.lead); ok && (b < lo || b > hi) {
			bad = true
		}
	}
	if bad {
		t.orphan(0, t.start, t.part)
		t.need, t.part = 0, 0
		if t.fatal == nil {
			t.b8(b, abs)
		}
		return
	}
	t.cp = t.cp<<6 | rune(b&0x3F)
	t.part++
	t.need--
	if t.need == 0 {
		cp := t.cp
		t.need, t.part = 0, 0
		t.emit(cp, true, false, t.start, t.cls)
	}
}

// RunWindow 在 data 上只转码起点落在 [Start,End) 的单元；仅 Last 段处理 EOF。
func RunWindow(data []byte, c Config, w Window) ([]byte, Stats, int64, error) {
	return nil, Stats{}, 0, nil
}
