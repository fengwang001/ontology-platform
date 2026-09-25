// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 Transcoder 实例不是并发安全的：同一时刻只能有一个 Write/Close。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// 源/目标编码。
const (
	UTF8 = iota
	UTF16LE
	UTF16BE
	UTF16 // 源专用：按流首 BOM 自动判定，无 BOM 视为 LE
)

var (
	// ErrOutputLimit 在标量边界上因输出越过上限时返回（非终态）。
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	// ErrClosed 在终态（严格出错或已关闭）后再次 Write/Close 时返回。
	ErrClosed = errors.New("stream: write on terminal transcoder")
)

// IllegalError 是严格模式下确定非法单元的错误。
type IllegalError struct{ Offset, Length int }

func (e *IllegalError) Error() string { return "stream: illegal unit" }

// TruncatedError 是 Close 时合法前缀被截断的错误，与 IllegalError 可区分。
type TruncatedError struct{ Offset, Length int }

func (e *TruncatedError) Error() string { return "stream: truncated input" }

// Config 配置转码器。SkipPrefix/NoTail/SkipN 仅供 par 使用。
type Config struct {
	Src, Dst   int
	Strict     bool
	MaxOut     int  // 输出字节上限，0 表示不限
	EmitBOM    bool // 输出开头是否写目标 BOM
	SkipN      int  // 前 SkipN 个源字节只驱动状态，不输出、不统计
	NoTail     bool // Close 时不结算 EOF 残尾
	SkipPrefix bool
}

// Stats 为可读出的统计。
type Stats struct {
	Valid, ValidBytes, Invalid, InvalidBytes, BOMBytes, Consumed, Checks int64
}

type unit struct {
	r       rune
	illegal bool
	n       int
	start   int64
}

// Transcoder 是流式转码器（非并发安全）。
type Transcoder struct {
	cfg      Config
	d8       u8.Decoder
	d16      u16.Decoder
	head     []byte // BOM 预读（0..3）
	decided  bool
	ready    []unit
	pendRaw  []byte // 已进入解码器但尚未随单元输出的原始字节（顺序前缀）
	out      []byte
	closed   bool
	terminal error
	srcOff   int64 // 已结算源字节（含 BOM）
	emitSrc  int64 // 已输出单元对应的源字节（含 BOM）
	st       Stats
}

// New 按 cfg 构造转码器。
func New(cfg Config) *Transcoder {
	return &Transcoder{cfg: cfg, d16: *u16.NewDecoder(cfg.Src == UTF16BE)}
}

// Stats 返回当前统计快照。
func (t *Transcoder) Stats() Stats {
	s := t.st
	s.Consumed = t.srcOff
	if t.cfg.Src == UTF8 {
		s.Checks = t.d8.Checks
	} else {
		s.Checks = t.d16.Checks
	}
	return s
}

// Output 返回已输出字节。
func (t *Transcoder) Output() []byte { return t.out }

// Resume 返回未结算残尾（BOM 残片 + 解码器残尾），供切到新实例续传。
func (t *Transcoder) Resume() []byte {
	var p []byte
	if !t.decided {
		p = append(p, t.head...)
	}
	if t.decided {
		p = append(p, t.pendRaw...)
	}
	return p
}

func (t *Transcoder) feed(b byte) {
	t.pendRaw = append(t.pendRaw, b)
	base := t.srcOff
	n := 0
	if t.cfg.Src == UTF8 {
		for _, e := range t.d8.Feed(b) {
			t.ready = append(t.ready, unit{e.R, e.Illegal, e.N, base})
			base += int64(e.N)
			n += e.N
		}
	} else {
		for _, e := range t.d16.Feed(b) {
			t.ready = append(t.ready, unit{e.R, e.Illegal, e.N, base})
			base += int64(e.N)
			n += e.N
		}
	}
	t.srcOff += int64(n)
}

func (t *Transcoder) encode(r rune) []byte {
	switch t.cfg.Dst {
	case UTF16LE:
		return u16.Encode(r, false)
	case UTF16BE:
		return u16.Encode(r, true)
	default:
		return u8.Encode(r)
	}
}

func (t *Transcoder) bomBytes() []byte {
	if t.cfg.Dst == UTF16LE {
		return u16.BOMLE
	}
	if t.cfg.Dst == UTF16BE {
		return u16.BOMBE
	}
	return []byte{0xEF, 0xBB, 0xBF}
}

var u8BOM = []byte{0xEF, 0xBB, 0xBF}

// Write 喂入输入；返回值为本次已确认输出对应的源字节增量。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil || t.closed {
		return 0, ErrClosed
	}
	startEmit := t.emitSrc
	for _, b := range p {
		if !t.decided {
			t.head = append(t.head, b)
			need := 2
			if t.cfg.Src == UTF8 {
				need = 3
			}
			if len(t.head) < need {
				continue
			}
			t.flushHead()
		}
		t.feed(b)
		if err := t.process(); err != nil {
			return int(t.emitSrc - startEmit), err
		}
	}
	return int(t.emitSrc - startEmit), nil
}

func (t *Transcoder) flushHead() {
	t.decided = true
	var bom []byte
	if t.cfg.Src == UTF16 {
		switch {
		case eq(t.head, u16.BOMLE):
			bom, t.d16 = u16.BOMLE, *u16.NewDecoder(false)
		case eq(t.head, u16.BOMBE):
			bom, t.d16 = u16.BOMBE, *u16.NewDecoder(true)
		default:
			t.d16 = *u16.NewDecoder(false)
		}
	} else if t.cfg.Src == UTF8 && eq(t.head, u8BOM) {
		bom = u8BOM
	} else if t.cfg.Src == UTF16LE && eq(t.head, u16.BOMLE) {
		bom = u16.BOMLE
	} else if t.cfg.Src == UTF16BE && eq(t.head, u16.BOMBE) {
		bom = u16.BOMBE
	}
	if bom != nil {
		t.srcOff += int64(len(bom))
		t.emitSrc += int64(len(bom))
		t.st.BOMBytes += int64(len(bom))
		t.head = t.head[len(bom):]
		if t.cfg.EmitBOM && !t.cfg.SkipPrefix {
			t.out = append(t.out, t.bomBytes()...)
		}
	}
	for _, b := range t.head {
		t.feed(b)
	}
	t.head = nil
}

func eq(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || a[0] == b[0] && a[1] == b[1] &&
		(len(a) == 2 || a[2] == b[2]))
}

func (t *Transcoder) process() error {
	for len(t.ready) > 0 {
		u := t.ready[0]
		if t.cfg.SkipPrefix && u.start < int64(t.cfg.SkipN) {
			t.ready = t.ready[1:]
			t.pendRaw = t.pendRaw[u.n:]
			continue
		}
		if u.illegal {
			t.st.Invalid++
			t.st.InvalidBytes += int64(u.n)
			if t.cfg.Strict {
				t.terminal = &IllegalError{Offset: int(u.start), Length: u.n}
				return t.terminal
			}
			u.r = scalar.ReplacementRune
		} else {
			t.st.Valid++
			t.st.ValidBytes += int64(u.n)
		}
		enc := t.encode(u.r)
		if t.cfg.MaxOut > 0 && len(t.out)+len(enc) > t.cfg.MaxOut {
			return ErrOutputLimit
		}
		t.out = append(t.out, enc...)
		t.emitSrc += int64(u.n)
		t.ready = t.ready[1:]
		t.pendRaw = t.pendRaw[u.n:]
	}
	return nil
}

// Close 结算残尾；严格模式下截断返回 *TruncatedError。
func (t *Transcoder) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	if !t.decided {
		if t.cfg.Src == UTF16 {
			t.d16 = *u16.NewDecoder(false)
		}
		t.flushHead()
	}
	if err := t.process(); err != nil {
		if errors.Is(err, ErrOutputLimit) {
			t.closed = false
		}
		return err
	}
	if t.cfg.NoTail {
		return nil
	}
	if t.cfg.Src == UTF8 {
		if ev, trunc := t.d8.Flush(); ev.N > 0 {
			if trunc {
				t.terminal = &TruncatedError{Offset: int(t.srcOff), Length: ev.N}
				return t.terminal
			}
			t.ready = append(t.ready, unit{ev.R, true, ev.N, t.srcOff})
			return t.process()
		}
		return nil
	}
	if _, odd, orphan := t.d16.Flush(); odd || orphan {
		t.terminal = &TruncatedError{Offset: int(t.srcOff), Length: 1 + b2i(orphan)}
		return t.terminal
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
