// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Direction 是转码方向。
type Direction int

const (
	U8ToU16LE Direction = iota
	U8ToU16BE
	U16LEToU8
	U16BEToU8
)

// Config 配置转码器。
type Config struct {
	Dir         Direction
	Strict      bool // 严格模式
	EmitBOM     bool // 流首 BOM 是否输出为 U+FEFF
	OutputLimit int  // 输出字节上限；<=0 表示不限
	MaxCache    int  // 切分缓存硬上限（>=3）
	NoBOM       bool // 流首 BOM 不按 BOM 识别（par 非首段用）
}

// 哨兵错误。
var (
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	ErrTerminal    = errors.New("stream: write after terminal error")
)

// InvalidError 携带非法单元在整个输入流中的起始偏移与长度。
type InvalidError struct{ Offset, Len int }

func (e *InvalidError) Error() string { return "stream: invalid byte sequence" }

// TruncatedError 与非法错误可区分；Kind：0=u8 前缀 1=u16 奇字节 2=孤立高代理。
type TruncatedError struct{ Offset, Len, Kind int }

func (e *TruncatedError) Error() string { return "stream: truncated input" }

// Stats 是字节守恒统计：ValidInput+InvalidBytes+BOMBytes==Consumed。
type Stats struct {
	ValidUnits   int
	InvalidUnits int
	InvalidBytes int
	BOMBytes     int
	Consumed     int
}

// Transcoder 是流式转码器；非并发安全。
type Transcoder struct {
	cfg      Config
	out      []byte
	totalFed int // 已喂入解码器字节数（含缓存）
	consumed int // 已形成完整单元并提交的字节数
	u8d      *u8.Decoder
	u16d     *u16.Decoder
	bom      bool
	stats    Stats
	checks   int64 // 非导出：字节被检查总次数
	term     error
	closed   bool
}

// New 按 cfg 创建转码器。
func New(cfg Config) *Transcoder {
	if cfg.MaxCache < 3 {
		cfg.MaxCache = 3
	}
	t := &Transcoder{cfg: cfg}
	switch cfg.Dir {
	case U8ToU16LE, U8ToU16BE:
		t.u8d = u8.NewDecoder()
	case U16LEToU8:
		t.u16d = u16.NewDecoder(u16.LE)
	default:
		t.u16d = u16.NewDecoder(u16.BE)
	}
	return t
}

func (t *Transcoder) encode(r scalar.Rune) {
	switch t.cfg.Dir {
	case U8ToU16LE:
		t.out = u16.Encode(t.out, r, u16.LE)
	case U8ToU16BE:
		t.out = u16.Encode(t.out, r, u16.BE)
	default:
		t.out = u8.Encode(t.out, r)
	}
}

func (t *Transcoder) encLen(r scalar.Rune) int {
	if t.cfg.Dir == U8ToU16LE || t.cfg.Dir == U8ToU16BE {
		return u16.EncLen(r)
	}
	return u8.EncLen(r)
}

// Output 返回已输出字节。
func (t *Transcoder) Output() []byte { return t.out }

// Checks 返回字节被检查总次数。
func (t *Transcoder) Checks() int64 { return t.checks }

// Cached 返回当前跨切分缓存字节数。
func (t *Transcoder) Cached() int { return t.totalFed - t.consumed }

// Stat 返回统计快照。
func (t *Transcoder) Stat() Stats {
	s := t.stats
	s.Consumed = t.consumed
	return s
}

type evt struct {
	r           scalar.Rune
	valid       bool
	off, length int
}

// Write 喂入字节；n 是本次提交（完整单元）字节数，调用方用 p[n:] 续传。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	if t.closed {
		return 0, t.mark(ErrTerminal)
	}
	t.checks += int64(len(p))
	base := t.totalFed
	t.totalFed += len(p)
	var evs []evt
	if t.u8d != nil {
		t.u8d.Feed(p, base, func(u u8.Unit) {
			evs = append(evs, evt{u.R, u.Valid, u.Off, u.Len})
		})
	} else {
		t.u16d.Feed(p, base, func(u u16.Unit) {
			evs = append(evs, evt{u.R, u.Valid, u.Off, u.Len})
		})
	}
	commit := 0
	for _, e := range evs {
		if !t.cfg.NoBOM && !t.bom && e.r == scalar.BOM {
			t.bom = true
			t.stats.BOMBytes += e.length
			commit += e.length
			if t.cfg.EmitBOM {
				if err := t.put(scalar.BOM); err != nil {
					return commit - e.length, t.mark(err)
				}
			}
			continue
		}
		if !e.valid {
			t.stats.InvalidUnits++
			t.stats.InvalidBytes += e.length
			if t.cfg.Strict {
				return commit, t.mark(&InvalidError{Offset: e.off, Len: e.length})
			}
			e.r = scalar.Replacement
		} else {
			t.stats.ValidUnits++
		}
		if err := t.put(e.r); err != nil {
			return commit, t.mark(err)
		}
		commit += e.length
	}
	t.consumed += commit
	return commit, nil
}

func (t *Transcoder) put(r scalar.Rune) error {
	if lim := t.cfg.OutputLimit; lim > 0 && len(t.out)+t.encLen(r) > lim {
		return ErrOutputLimit
	}
	t.encode(r)
	return nil
}

func (t *Transcoder) mark(e error) error {
	t.term = e
	return e
}

// Close 处理流结束残留；截断与非法可区分。
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	t.closed = true
	flushTrunc := func(off, n, kind int) error {
		t.stats.InvalidUnits++
		t.stats.InvalidBytes += n
		if t.cfg.Strict {
			return t.mark(&TruncatedError{Offset: off, Len: n, Kind: kind})
		}
		if err := t.put(scalar.Replacement); err != nil {
			return t.mark(err)
		}
		t.consumed += n
		return nil
	}
	if t.u8d != nil {
		if off, n, trunc := t.u8d.Flush(); trunc {
			return flushTrunc(off, n, 0)
		}
	} else if off, n, kind := t.u16d.Flush(); kind >= 0 {
		return flushTrunc(off, n, kind)
	}
	return nil
}
