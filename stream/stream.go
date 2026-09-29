// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的；如需并发请使用 par 包。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u8"
	"ontology/u16"
)

// Encoding 标识字节编码。
type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

const bomRune = rune(0xFEFF)

// Config 配置转码器。Strict 为真时遇到非法单元返回错误；Limit>0 为输出字节上限；
// EmitBOM 为真时在输出开头写出目标编码 BOM（且丢弃输入流首 BOM）。
type Config struct {
	From, To Encoding
	Strict   bool
	Limit    int
	EmitBOM  bool
}

var (
	// ErrInvalid 非法单元（配合 UnitError 使用）。
	ErrInvalid = errors.New("stream: invalid unit")
	// ErrTruncated 流结束时残留半个字符（配合 UnitError 使用）。
	ErrTruncated = errors.New("stream: truncated input")
	// ErrLimit 输出会越过上限，停在标量边界。
	ErrLimit = errors.New("stream: output limit exceeded")
	// ErrTerminal 实例已进入终态后再次写入。
	ErrTerminal = errors.New("stream: transcoder in terminal state")
)

// UnitError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Kind   error
	Offset int64
	Length int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

// Stats 是字节守恒统计。
type Stats struct {
	Scalars      int64 // 已接受的合法标量数（不含 BOM）
	InvalidUnits int64 // 非法单元数
	InvalidBytes int64 // 非法单元吞掉的字节数
	BOMBytes     int64 // 流首 BOM 字节数
	Consumed     int64 // 已消费输入总字节数（不含跨 Write 缓存前缀）
}

// Transcoder 是一次性流式转码器。
type Transcoder struct {
	cfg        Config
	out        []byte
	d8         *u8.Decoder
	d16        *u16.Decoder
	stats      Stats
	bomWritten bool
	terminal   bool
	termErr    error
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	if cfg.From == UTF8 {
		t.d8 = &u8.Decoder{}
	} else {
		ord := u16.BigEndian
		if cfg.From == UTF16LE {
			ord = u16.LittleEndian
		}
		t.d16 = u16.NewDecoder(ord)
	}
	return t
}

// Write 喂入输入字节，返回已完整结算（对应已输出标量）的字节数。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal {
		return 0, t.termErr
	}
	consumed := 0
	for i := 0; i < len(p); i++ {
		off := t.stats.Consumed
		size, r, bad, repro, bom := t.feed(p[i])
		for repro { // u8/u16 都可能要求把同一字节再结算一次
			size, r, bad, repro, bom = t.feed(p[i])
		}
		if size == 0 {
			continue
		}
		if bom {
			t.stats.Consumed += int64(size)
			t.stats.BOMBytes += int64(size)
			consumed += size
			continue
		}
		t.stats.Consumed += int64(size)
		consumed += size
		if bad {
			t.stats.InvalidUnits++
			t.stats.InvalidBytes += int64(size)
			if t.cfg.Strict {
				e := &UnitError{Kind: ErrInvalid, Offset: off, Length: size}
				t.die(e, i, consumed, p)
				return consumed, e
			}
			r = scalar.ReplacementRune
		} else {
			if r == bomRune && t.firstUnit() { // 流首 UTF-8 BOM
				t.stats.BOMBytes += int64(size)
				if !t.cfg.EmitBOM {
					continue
				}
				r = bomRune
			} else {
				t.stats.Scalars++
			}
		}
		if err := t.emitBOMOnce(); err != nil {
			t.die(err)
			return consumed, err
		}
		if r == bomRune && t.firstUnit() && t.cfg.From == UTF8 && t.cfg.EmitBOM {
			// BOM 已在 emitBOMOnce 输出，避免重复
			continue
		}
		if err := t.emit(r); err != nil {
			t.die(err)
			return consumed, err
		}
	}
	return consumed, nil
}

func (t *Transcoder) firstUnit() bool {
	return t.stats.Scalars == 0 && t.stats.InvalidUnits == 0 && t.stats.BOMBytes == 0
}

func (t *Transcoder) emitBOMOnce() error {
	if !t.cfg.EmitBOM || t.bomWritten {
		return nil
	}
	t.bomWritten = true
	return t.emit(bomRune)
}

func (t *Transcoder) die(err error) {
	t.terminal, t.termErr = true, err
}

func (t *Transcoder) feed(b byte) (int, rune, bool, bool, bool) {
	if t.d8 != nil {
		s, r, bad, repro := t.d8.Push(b)
		return s, r, bad, repro, false
	}
	return t.d16.Push(b)
}

func (t *Transcoder) pending() int {
	if t.d8 != nil {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}

func (t *Transcoder) emit(r rune) error {
	enc := make([]byte, 0, 4)
	if t.cfg.To == UTF8 {
		enc = u8.Encode(enc, r)
	} else {
		enc = u16.Encode(enc, r, t.cfg.To == UTF16LE)
	}
	if t.cfg.Limit > 0 && len(t.out)+len(enc) > t.cfg.Limit {
		return ErrLimit
	}
	t.out = append(t.out, enc...)
	return nil
}

// Close 在流结束时结算残留前缀。
func (t *Transcoder) Close() error {
	if t.terminal {
		return t.termErr
	}
	off := t.stats.Consumed
	var size int
	var bad, truncated bool
	if t.d8 != nil {
		size, bad = t.d8.Finish()
	} else {
		size, bad, truncated = t.d16.Finish()
	}
	if size == 0 {
		t.terminal = true
		return nil
	}
	t.stats.Consumed += int64(size)
	e := &UnitError{Kind: ErrInvalid, Offset: off, Length: size}
	if truncated {
		e.Kind = ErrTruncated
	}
	if t.cfg.Strict || truncated {
		t.terminal, t.termErr = true, e
		return e
	}
	t.stats.InvalidUnits++
	t.stats.InvalidBytes += int64(size)
	err := t.emit(scalar.ReplacementRune)
	t.terminal = true
	if err != nil {
		t.termErr = err
		return err
	}
	return nil
}

// Output 返回已产出的字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回守恒统计快照。
func (t *Transcoder) Stats() Stats { return t.stats }

// Checks 返回底层解码器累计的字节检查次数。
func (t *Transcoder) Checks() int64 {
	if t.d8 != nil {
		return t.d8.Checks
	}
	return t.d16.Checks
}
