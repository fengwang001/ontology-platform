package stream

import (
	"errors"
	"fmt"

	"ontology/u16"
	"ontology/u8"
)

// From 是输入编码。
type From int

const (
	FromUTF8 From = iota
	FromUTF16LE
	FromUTF16BE
	FromUTF16 // 字节序仅由流首 BOM 决定，无 BOM 时按 LE。
)

// To 是输出编码。
type To int

const (
	ToUTF8 To = iota
	ToUTF16LE
	ToUTF16BE
)

// 四类可判定错误：彼此用 errors.Is 区分。
var (
	ErrIllegal  = errors.New("stream: illegal code unit")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit    = errors.New("stream: output limit exceeded")
	ErrClosed   = errors.New("stream: transcoder already in terminal state")
)

// UnitError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Kind  error
	Start int64
	Len   int
}

func (e *UnitError) Error() string {
	return fmt.Sprintf("%v at byte %d len %d", e.Kind, e.Start, e.Len)
}
func (e *UnitError) Unwrap() error { return e.Kind }

// Stats 是字节守恒统计。
type Stats struct {
	Scalars    int64 // 已接受的合法标量数
	Illegals   int64 // 非法单元数
	IllegalIn  int64 // 被非法/截断单元吞掉的输入字节数
	BOMBytes   int64 // 流首 BOM 占用的输入字节数
	Consumed   int64 // 已消费（含 BOM）的输入总字节数
	OutBytes   int64 // 已产出的输出字节数
	Checks     int64 // 字节被检查的总次数
	MaxPending int   // 历史上未决缓存的峰值
}

// Config 配置转码器。
type Config struct {
	Src From
	Dst To
	// Strict 为 true 时遇第一个非法单元即进入终态。
	Strict bool
	// EmitBOM 为 true 时把流首 BOM 作为普通 U+FEFF 输出，否则丢弃。
	EmitBOM bool
	// MaxOut 为输出字节上限；<=0 表示不限。
	MaxOut int64
}

type srcEvent struct {
	start int64
	n     int
	r     rune
	valid bool
	bom   bool
	trunc bool
	repl  bool // 该合法标量是由非法单元替换而来
}

type decoder interface {
	decode(p []byte, emit func(srcEvent) bool)
	flush() *srcEvent
	pos() int64
	pending() int
	checks() int64
}

// Transcoder 是流式转码器。单个实例不是并发安全的。
type Transcoder struct {
	cfg Config
	dec decoder
	out []byte

	terminal bool
	fatal    error

	scalars  int64
	illegals int64
	illBytes int64
	bomBytes int64
	maxPend  int
}

// New 按 cfg 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	switch cfg.Src {
	case FromUTF8:
		t.dec = &u8Dec{d: &u8.Decoder{}}
	case FromUTF16LE:
		t.dec = &u16Dec{d: u16.NewDecoder(u16.LE)}
	case FromUTF16BE:
		t.dec = &u16Dec{d: u16.NewDecoder(u16.BE)}
	default:
		t.dec = &u16Dec{d: u16.NewDecoder(u16.Unset)}
	}
	return t
}

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.Dst == ToUTF8 {
		return u8.EncodedLen(r)
	}
	return u16.EncodedLen(r)
}

func (t *Transcoder) encode(r rune) {
	if t.cfg.Dst == ToUTF8 {
		t.out = u8.Encode(t.out, r)
	} else {
		o := u16.LE
		if t.cfg.Dst == ToUTF16BE {
			o = u16.BE
		}
		t.out = u16.Encode(t.out, r, o)
	}
}

// handle 处理一个解码单元，返回 false 表示到达输出上限（终态）。
func (t *Transcoder) handle(ev srcEvent) bool {
	if ev.bom {
		t.bomBytes += int64(ev.n)
		if t.cfg.EmitBOM {
			if !t.room(ev.n) {
				return false
			}
			t.encode(0xFEFF)
		}
		return true
	}
	if !ev.valid {
		kind := ErrIllegal
		if ev.trunc {
			kind = ErrTruncated
		}
		if t.cfg.Strict {
			t.fatal = &UnitError{Kind: kind, Start: ev.start, Len: ev.n}
			t.terminal = true
			return false
		}
		t.illegals++
		t.illBytes += int64(ev.n)
		ev.r, ev.repl = 0xFFFD, true
		ev.valid = true
	}
	if ev.valid {
		n := t.encLen(ev.r)
		if !t.room(n) {
			return false
		}
		t.encode(ev.r)
		if !ev.repl {
			t.scalars++
		}
	}
	return true
}

func (t *Transcoder) room(n int) bool {
	if t.cfg.MaxOut > 0 && int64(len(t.out))+int64(n) > t.cfg.MaxOut {
		t.fatal = ErrLimit
		t.terminal = true
		return false
	}
	return true
}

func (t *Transcoder) notePending() {
	if p := t.dec.pending(); p > t.maxPend {
		t.maxPend = p
	}
}

// Write 喂入一段输入，返回已消费字节数。
// 严格模式出错或输出超限时进入终态；返回的 n 恰好对应已输出的标量，
// 残留在切分缓存中的字节不计入 n。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal {
		return 0, t.fatalOrClosed()
	}
	startPos := t.dec.pos()
	stopped := false
	t.dec.decode(p, func(ev srcEvent) bool {
		if !t.handle(ev) {
			stopped = true
			return false
		}
		t.notePending()
		return true
	})
	t.notePending()
	if stopped {
		consumed := t.dec.pos() - int64(t.dec.pending())
		return int(consumed - startPos), t.fatal
	}
	return len(p), nil
}

func (t *Transcoder) fatalOrClosed() error {
	if t.fatal != nil {
		return t.fatal
	}
	return ErrClosed
}

// Close 宣告流结束：替换模式输出最后一个 FFFD（若有残留），
// 严格模式对残留返回可与非法字节区分的截断错误。之后进入终态。
func (t *Transcoder) Close() error {
	if t.terminal {
		return t.fatalOrClosed()
	}
	if ev := t.dec.flush(); ev != nil {
		t.handle(*ev)
	}
	t.terminal = true
	if t.fatal != nil {
		return t.fatal
	}
	return nil
}

// Output 返回已产出的输出字节（调用方不得在继续写入期间修改）。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回字节守恒统计。
func (t *Transcoder) Stats() Stats {
	consumed := t.dec.pos() - int64(t.dec.pending())
	return Stats{
		Scalars:    t.scalars,
		Illegals:   t.illegals,
		IllegalIn:  t.illBytes,
		BOMBytes:   t.bomBytes,
		Consumed:   consumed,
		OutBytes:   int64(len(t.out)),
		Checks:     t.dec.checks(),
		MaxPending: t.maxPend,
	}
}
