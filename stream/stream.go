// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的。
package stream

import (
	"errors"
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Format 是编码格式。
type Format uint8

const (
	U8 Format = iota
	U16LE
	U16BE
)

// 可判定哨兵错误。
var (
	ErrIllegal   = errors.New("stream: illegal byte unit")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrClosed    = errors.New("stream: write after terminal state")
)

// OffsetError 携带错误单元在整个输入流中的起始字节偏移与长度。
type OffsetError struct {
	Err    error
	Offset int64
	Len    int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Stats 为字节守恒统计；Checks 是字节被检查总次数，MaxPending 是缓存峰值。
type Stats struct {
	Scalars    int64
	BadUnits   int64
	BadBytes   int64
	BOMBytes   int64
	Consumed   int64
	Checks     int64
	MaxPending int
}

// Config 配置转码器。
type Config struct {
	From, To Format
	Strict   bool
	EmitBOM  bool
	Limit    int64
}

type evKind uint8

const (
	evNone evKind = iota
	evRune
	evBad
)

// Transcoder 为流式转码器。
type Transcoder struct {
	cfg   Config
	out   []byte
	st    Stats
	term  error
	d8    u8.Decoder
	d16   *u16.Decoder
	order u16.Order

	cons  int64 // 已稳定消费（不含挂起前缀与 BOM 试探字节）
	pend  []byte
	pend0 int64 // pend 首字节的流偏移

	bom   []byte // 流首试探
	bomOK bool   // BOM 判定已结束
}

// New 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, bom: make([]byte, 0, 3), pend: make([]byte, 0, 3)}
	if cfg.From == U16LE {
		t.order, t.d16 = u16.LE, u16.NewDecoder(u16.LE)
	} else if cfg.From == U16BE {
		t.order, t.d16 = u16.BE, u16.NewDecoder(u16.BE)
	}
	return t
}

// Output 返回已产生输出的副本。
func (t *Transcoder) Output() []byte {
	o := make([]byte, len(t.out))
	copy(o, t.out)
	return o
}

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.st }

func (t *Transcoder) enc(r rune) []byte {
	if t.cfg.To == U8 {
		return u8.Encode(r)
	}
	o := u16.LE
	if t.cfg.To == U16BE {
		o = u16.BE
	}
	return u16.Encode(r, o)
}

func (t *Transcoder) step(b byte) (evKind, rune, bool) {
	if t.cfg.From == U8 {
		e, r, u := t.d8.Step(b)
		return evKind(e), r, u
	}
	e, r, u := t.d16.Step(b)
	return evKind(e), r, u
}

// Write 喂入字节，返回本次稳定消费字节数。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	startCons := t.cons
	i := 0
	for i < len(p) {
		b := p[i]
		t.st.Checks++
		// 流首 BOM 试探
		if !t.bomOK {
			stop, err := t.tryBOM(b)
			if stop {
				return i, err
			}
			i++
			continue
		}
		if len(t.pend) == 0 {
			t.pend0 = t.cons
		}
		k, r, used := t.step(b)
		if k == evNone {
			t.pend = append(t.pend, b)
			if len(t.pend) > t.st.MaxPending {
				t.st.MaxPending = len(t.pend)
			}
			i++
			continue
		}
		if used {
			t.pend = append(t.pend, b)
		}
		// k 为 evRune/evBad：t.pend 为该单元字节；used=false 时 b 须退回给下一个单元
		err := t.finish(k, r)
		i++ // used=false 时下面 continue 回退到同一 i
		if err != nil {
			return int(t.pend0 - startCons), err
		}
		if !used {
			i-- // b 成为新单元首字节，在同一位置重新处理
		}
	}
	return int(t.cons - startCons), nil
}

// finish 定稿一个事件；调用前 t.pend 为该单元全部字节。
func (t *Transcoder) finish(k evKind, r rune) error {
	unit := len(t.pend)
	if k == evBad {
		t.st.BadUnits++
		t.st.BadBytes += int64(unit)
		if t.cfg.Strict {
			t.term = &OffsetError{Err: ErrIllegal, Offset: t.pend0, Len: unit}
			t.pend = t.pend[:0]
			return t.term // 单元未稳定消费；调用方改新实例从该偏移续传
		}
		r = scalar.Replacement
	} else {
		t.st.Scalars++
	}
	b := t.enc(r)
	if t.cfg.Limit > 0 && int64(len(t.out)+len(b)) > t.cfg.Limit {
		t.undo(k)
		t.term = &OffsetError{Err: ErrLimit, Offset: t.pend0, Len: len(b)}
		t.pend = t.pend[:0]
		return t.term
	}
	t.out = append(t.out, b...)
	t.cons += int64(unit)
	t.st.Consumed = t.cons
	t.pend = t.pend[:0]
	return nil
}

func (t *Transcoder) undo(k evKind) {
	if k == evBad {
		t.st.BadUnits--
		t.st.BadBytes -= int64(len(t.pend))
	} else {
		t.st.Scalars--
	}
}

// Close 收尾挂起前缀。
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	n := len(t.pend)
	off := t.pend0
	if !t.bomOK && len(t.bom) > 0 {
		n, off = len(t.bom), t.cons
	}
	if n == 0 {
		return nil
	}
	if t.cfg.Strict {
		t.term = &OffsetError{Err: ErrTruncated, Offset: off, Len: n}
		return t.term
	}
	t.st.BadUnits++
	t.st.BadBytes += int64(n)
	t.out = append(t.out, t.enc(scalar.Replacement)...)
	t.cons += int64(n)
	t.st.Consumed = t.cons
	t.pend = t.pend[:0]
	t.bom = t.bom[:0]
	t.bomOK = true
	return nil
}
