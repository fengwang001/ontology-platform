// Package stream 实现带非法字节替换的流式 UTF-8/UTF-16 转码器。非并发安全。
package stream

import (
	"errors"
	"ontology/u16"
)

type Direction int

const U8ToU8, U8To16LE, U8To16BE, U16LEToU8, U16BEToU8 Direction = 0, 1, 2, 3, 4

// Config：Strict 严格模式；KeepBOM 保留流首 BOM；MaxOut>0 为输出字节上限。
type Config struct {
	Dir             Direction
	Strict, KeepBOM bool
	MaxOut          int
}

// Stats 为字节守恒统计；非导出字段 checks 为字节被检查总次数。
type Stats struct {
	Scalars, Bad, InBytes, BadBytes, BOMBytes, checks int64
}

func (s Stats) Checks() int64 { return s.checks }

const MaxPending = 3 // 未完成前缀缓存硬上限（UTF-8 最长 4 字节）

var (
	ErrBadUnit   = errors.New("stream: invalid code unit sequence")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrClosed    = errors.New("stream: writer already closed")
)

// UnitError 携带单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Kind   error
	Offset int64
	Len    int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

// Transcoder 是流式转码器（非并发安全），用 New 构造。
type Transcoder struct {
	cfg                          Config
	out                          []byte
	st                           Stats
	pend, head                   []byte
	hi                           bool
	order                        u16.Order
	headDone, closed             bool
	terr                         error
	consumed, pendStart, baseOff int64
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, order: u16.LE, pend: make([]byte, 0, 3), head: make([]byte, 0, 3)}
	if cfg.Dir == U16BEToU8 {
		t.order = u16.BE
	}
	return t
}

// Prime 供并行分段：carry 为应在本段解析的前段尾部，globalOff 为其全局偏移。
func (t *Transcoder) Prime(carry []byte, globalOff int64) {
	t.headDone, t.baseOff, t.consumed = true, globalOff, globalOff
	t.pend, t.pendStart = append(t.pend, carry...), globalOff
}

func (t *Transcoder) Stats() Stats     { return t.st }
func (t *Transcoder) Output() []byte   { return t.out }
func (t *Transcoder) PendingLen() int  { return len(t.pend) }
func (t *Transcoder) Committed() int64 { return t.consumed }
func (t *Transcoder) is16In() bool     { return t.cfg.Dir >= U16LEToU8 }

// Write 喂入字节；超限续传断点用 Committed()（未完成前缀读入但未提交）。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terr != nil {
		return 0, t.terr
	}
	if t.closed {
		return 0, ErrClosed
	}
	n := 0
	for n < len(p) {
		t.st.checks++
		for {
			var adv bool
			if !t.headDone {
				adv = t.feedHead(p[n])
			} else if t.is16In() {
				adv = t.feed16(p[n])
			} else {
				adv = t.feed8(p[n])
			}
			if adv {
				n++
				break
			}
			if t.terr != nil {
				return n, t.terr
			}
		}
		if t.terr != nil {
			return n, t.terr
		}
	}
	return n, nil
}

// Close 宣告流结束；残留未完成前缀替换或报截断错误。
func (t *Transcoder) Close() error {
	if t.closed {
		return ErrClosed
	}
	if t.terr != nil {
		return t.terr
	}
	t.closed = true
	if !t.headDone {
		if t.is16In() && len(t.head) == 1 {
			return t.fail(ErrTruncated, t.baseOff, 1)
		}
		h := append([]byte(nil), t.head...)
		t.head, t.headDone = t.head[:0], true
		t.replay(h)
	}
	if t.terr == nil && len(t.pend) > 0 {
		l := len(t.pend)
		if t.cfg.Strict {
			return t.fail(ErrTruncated, t.pendStart, l)
		}
		t.badUnit(t.pendStart, l)
		t.pend, t.hi = t.pend[:0], false
	}
	return t.terr
}

func (t *Transcoder) fail(k error, off int64, l int) error {
	if k == ErrLimit {
		t.terr = ErrLimit
	} else {
		t.terr = &UnitError{Kind: k, Offset: off, Len: l}
	}
	return t.terr
}
