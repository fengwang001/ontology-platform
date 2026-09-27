// Package stream 实现带非法字节替换的流式 UTF-8/UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的。
package stream

import (
	"errors"
	"fmt"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

var (
	ErrInvalidByte = errors.New("stream: invalid byte unit")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	ErrTerminal    = errors.New("stream: write after terminal state")
)

// ByteError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type ByteError struct {
	Kind   error
	Offset int
	Len    int
}

func (e *ByteError) Error() string {
	return fmt.Sprintf("%v at offset %d len %d", e.Kind, e.Offset, e.Len)
}
func (e *ByteError) Unwrap() error { return e.Kind }

// LimitError 携带必须续传给新实例的字节（旧缓存+未消费尾部）。
type LimitError struct {
	Resume []byte
}

func (e *LimitError) Error() string { return ErrOutputLimit.Error() }
func (e *LimitError) Unwrap() error { return ErrOutputLimit }

type Format int

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
	UTF16Auto
)

type Config struct {
	From, To Format
	Strict   bool
	EmitBOM  bool // 流开头 BOM 是否作为 U+FEFF 输出
	Limit    int  // 输出字节上限，<=0 不限
}

type Stats struct {
	Scalars, BadUnits, BadBytes, BOMBytes, Consumed, OutBytes, Checks int
}

// Transcoder 是单线程流式转码器。
type Transcoder struct {
	cfg                                   Config
	order                                 u16.Order
	pending                               []byte
	absOff                                int
	scalars, badUnits, badBytes, bomBytes int
	out                                   []byte
	started, closed, terminal             bool
	termErr                               error
	checks                                int
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, pending: make([]byte, 0, 3)}
	if cfg.From == UTF16LE {
		t.order = u16.LE
	}
	if cfg.From == UTF16BE {
		t.order = u16.BE
	}
	return t
}

func (t *Transcoder) outOrder() u16.Order {
	if t.cfg.To == UTF16BE {
		return u16.BE
	}
	return u16.LE
}

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.To == UTF8 {
		return u8.EncLen(r)
	}
	return u16.EncLen(r)
}

func (t *Transcoder) doEmit(r rune) {
	if t.cfg.To == UTF8 {
		t.out = u8.Append(t.out, r)
	} else {
		t.out = u16.Append(t.out, r, t.outOrder())
	}
}

// accept 接收一个完整单元。stop=true 表示因超限应中止本单元。
func (t *Transcoder) accept(r rune, valid, bom bool, start, size int) (stop bool, err error) {
	if bom && !t.started {
		t.bomBytes += size
		t.started = true
		if t.cfg.EmitBOM {
			if t.cfg.Limit > 0 && len(t.out)+t.encLen(0xFEFF) > t.cfg.Limit {
				return true, ErrOutputLimit
			}
			t.doEmit(0xFEFF)
		}
		return false, nil
	}
	t.started = true
	if !valid {
		t.badUnits++
		t.badBytes += size
		if t.cfg.Strict {
			return true, &ByteError{Kind: ErrInvalidByte, Offset: start, Len: size}
		}
		r = scalar.Surrogate
	}
	if t.cfg.Limit > 0 && len(t.out)+t.encLen(r) > t.cfg.Limit {
		return true, ErrOutputLimit
	}
	t.doEmit(r)
	if valid {
		t.scalars++
	}
	return false, nil
}

func (t *Transcoder) setTerm(err error) error {
	t.terminal, t.termErr = true, err
	return err
}

// Write 喂入输入。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal {
		return 0, t.termErr
	}
	if t.closed {
		return 0, ErrTerminal
	}
	oldPending := len(t.pending)
	t.pending = append(t.pending, p...)
	t.checks += len(p)
	if t.cfg.From == UTF16Auto {
		if o, ok := u16.DetectBOM(t.pending); ok {
			t.order = o
		} else {
			t.order = u16.LE
		}
	}
	i := 0
	if oldPending > 0 {
		i = 0 // 缓存前缀从整体缓冲起始重判
	}
	for i < len(t.pending) {
		var u u8.Unit
		var u16u u16.Unit
		isU16 := t.cfg.From != UTF8
		if isU16 {
			u16u = u16.DecodeOne(t.pending[i:], t.order)
			if u16u.Size == 0 {
				break
			}
		} else {
			u = u8.DecodeOne(t.pending[i:])
			if u.Size == 0 {
				break
			}
		}
		if isU16 && u16u.Partial {
			break
		}
		if !isU16 && u.Partial {
			break
		}
		if isU16 && u16u.Bad {
			stop, err := t.accept(scalar.Surrogate, false, false, t.absOff+i, 2)
			if stop {
				return t.halt(i, oldPending, len(p), err)
			}
			i += 2
			continue
		}
		var r rune
		var valid, bom bool
		var size int
		if isU16 {
			r, valid, bom, size = u16u.R, u16u.Valid, u16u.BOM, u16u.Size
		} else {
			r, valid, bom, size = u.R, u.Valid, u.BOM, u.Size
		}
		stop, err := t.accept(r, valid, bom, t.absOff+i, size)
		if stop {
			return t.halt(i, oldPending, len(p), err)
		}
		i += size
	}
	rem := append([]byte(nil), t.pending[i:]...)
	t.pending = t.pending[:0]
	t.pending = append(t.pending, rem...)
	t.absOff += i
	return len(p), nil
}

func (t *Transcoder) halt(i, oldP, pLen int, err error) (int, error) {
	resume := append([]byte(nil), t.pending[i:]...)
	t.pending = t.pending[:0]
	t.pending = append(t.pending, resume...)
	n := pLen
	if tail := len(resume) - oldP; tail > 0 {
		n = pLen - tail
	}
	if errors.Is(err, ErrOutputLimit) {
		return n, t.setTerm(&LimitError{Resume: resume})
	}
	t.absOff += i
	if n < 0 {
		n = 0
	}
	return n, t.setTerm(err)
}

// Close 结束输入。
func (t *Transcoder) Close() error {
	if t.terminal {
		return t.termErr
	}
	t.closed = true
	if len(t.pending) == 0 {
		return nil
	}
	if t.cfg.Strict {
		return t.setTerm(&ByteError{Kind: ErrTruncated, Offset: t.absOff, Len: len(t.pending)})
	}
	t.badUnits++
	t.badBytes += len(t.pending)
	need := 3
	if t.cfg.To != UTF8 {
		need = 2
	}
	if t.cfg.Limit > 0 && len(t.out)+need > t.cfg.Limit {
		return t.setTerm(&LimitError{Resume: append([]byte(nil), t.pending...)})
	}
	t.doEmit(scalar.Surrogate)
	t.absOff += len(t.pending)
	t.pending = t.pending[:0]
	return nil
}

func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) Stats() Stats {
	return Stats{t.scalars, t.badUnits, t.badBytes, t.bomBytes,
		t.absOff, len(t.out), t.checks}
}

func (t *Transcoder) PendingLen() int { return len(t.pending) }
