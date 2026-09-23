// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 不是并发安全的；并行使用见 par 包。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Direction 选择转码方向。U16AutoToU8 按开头 BOM 判定字节序，无 BOM 按 BE。
type Direction int

const (
	U8ToU16LE Direction = iota
	U8ToU16BE
	U16LEToU8
	U16BEToU8
	U8ToU8
	U16AutoToU8
)

var (
	ErrIllegal   = errors.New("stream: illegal byte unit")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrTerminal  = errors.New("stream: write after terminal state")
)

// PositionError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type PositionError struct {
	Err    error
	Offset int64
	Len    int
}

func (e *PositionError) Error() string { return e.Err.Error() }
func (e *PositionError) Unwrap() error { return e.Err }

// Config 配置转码器。EmitBOM 控制开头 BOM 是否作为 U+FEFF 输出；
// MaxOutput<=0 表示不限。
type Config struct {
	Dir       Direction
	Strict    bool
	EmitBOM   bool
	MaxOutput int
}

// Stats 是字节守恒统计（见 DESIGN 字节守恒等式）。
type Stats struct {
	Scalars   int64
	BadUnits  int64
	BadBytes  int64
	LegalBytes int64
	BOMBytes  int64
	Consumed  int64
	ByteCheck int64
}

// Transcoder 是单次转码的有状态机器。
type Transcoder struct {
	cfg                      Config
	little                   bool
	is16in, is16out, auto16  bool
	out, carry               []byte
	totalIn, closedBytes     int64
	bomDone                  bool
	limitErr, fatal          error
	stats                    Stats
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	t.is16in = cfg.Dir == U16LEToU8 || cfg.Dir == U16BEToU8 || cfg.Dir == U16AutoToU8
	t.auto16 = cfg.Dir == U16AutoToU8
	t.is16out = cfg.Dir == U8ToU16LE || cfg.Dir == U8ToU16BE
	t.little = cfg.Dir == U8ToU16LE || cfg.Dir == U16LEToU8
	if cfg.MaxOutput > 0 {
		t.out = make([]byte, 0, cfg.MaxOutput)
	}
	return t
}

func (t *Transcoder) Output() []byte { return t.out }
func (t *Transcoder) Err() error {
	if t.fatal != nil {
		return t.fatal
	}
	return t.limitErr
}

// Stats 返回统计快照；Consumed 为已闭合字节数（不含 carry）。
func (t *Transcoder) Stats() Stats {
	s := t.stats
	s.Consumed = t.closedBytes + s.BOMBytes
	return s
}

func (t *Transcoder) emit(r rune) bool {
	enc := u8.Encode(nil, r)
	if t.is16out {
		enc = u16.Encode(nil, r, t.little)
	}
	if t.cfg.MaxOutput > 0 && len(t.out)+len(enc) > t.cfg.MaxOutput {
		return false
	}
	t.out = append(t.out, enc...)
	return true
}

// Write 喂入字节，返回本次新闭合消费的字节数。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.fatal != nil {
		return 0, t.fatal
	}
	if t.limitErr != nil {
		return 0, ErrTerminal
	}
	startClosed := t.closedBytes + t.stats.BOMBytes
	t.totalIn += int64(len(p))
	t.carry = append(t.carry, p...)
	if !t.bomDone {
		t.handleBOM()
	}
	for {
		at, avail := 0, len(t.carry)
		if avail == 0 {
			break
		}
		var ku int
		var kr rune
		var kbad, kshort, looked int
		if t.is16in {
			u := u16.DecodeAt(t.carry, at, avail, t.little)
			ku, kr, looked = u.N, u.R, u.Looked
			kbad, kshort = b2i(u.Kind == u16.Bad), b2i(u.Kind == u16.Short)
			if kshort != 0 && !(avail == 1) && !(avail >= 2 && ku == 2) {
				kshort = 0
			}
		} else {
			u := u8.DecodeAt(t.carry, at, avail)
			ku, kr, looked = u.N, u.R, u.Looked
			kbad, kshort = b2i(u.Kind == u8.Bad), b2i(u.Kind == u8.Short)
		}
		t.stats.ByteCheck += int64(looked)
		if kshort != 0 {
			break // 全合法前缀，等待更多字节
		}
		off := t.totalIn - int64(avail)
		if kbad != 0 {
			if t.cfg.Strict {
				t.fatal = &PositionError{Err: ErrIllegal, Offset: off, Len: ku}
				t.advance(ku, false)
				return int(t.closedBytes + t.stats.BOMBytes - startClosed), t.fatal
			}
			if !t.emit(scalar.Replacement) {
				t.limitErr = ErrLimit
				return int(off - startClosed), t.limitErr
			}
			t.stats.BadUnits++
			t.stats.BadBytes += int64(ku)
			t.advance(ku, false)
			continue
		}
		if !t.emit(kr) {
			t.limitErr = ErrLimit
			return int(off - startClosed), t.limitErr
		}
		t.stats.Scalars++
		t.advance(ku, true)
	}
	return int(t.closedBytes + t.stats.BOMBytes - startClosed), nil
}

func (t *Transcoder) advance(n int, legal bool) {
	if legal {
		t.stats.LegalBytes += int64(n)
	}
	t.closedBytes += int64(n)
	t.carry = t.carry[n:]
}

// Close 收尾：残留截断前缀按策略处理。
func (t *Transcoder) Close() error {
	if t.fatal != nil {
		return t.fatal
	}
	if t.limitErr != nil {
		return t.limitErr
	}
	if len(t.carry) > 0 {
		off := t.totalIn - int64(len(t.carry))
		n := len(t.carry)
		if t.cfg.Strict {
			t.fatal = &PositionError{Err: ErrTruncated, Offset: off, Len: n}
			return t.fatal
		}
		if !t.emit(scalar.Replacement) {
			t.limitErr = ErrLimit
			return t.limitErr
		}
		t.stats.BadUnits++
		t.stats.BadBytes += int64(n)
		t.advance(n, false)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// handleBOM 只在流最开头识别一次 BOM；切开的 BOM 等待后续字节。
func (t *Transcoder) handleBOM() {
	if t.is16in {
		if len(t.carry) < 2 {
			return
		}
		if t.auto16 {
			t.little = t.carry[0] == 0xFF && t.carry[1] == 0xFE
		}
		u := u16.DecodeAt(t.carry, 0, len(t.carry), t.little)
		if u.Kind == u16.OK && u.R == 0xFEFF {
			t.consumeBOM(2)
			return
		}
		t.bomDone = true
		return
	}
	switch {
	case len(t.carry) >= 3 && t.carry[0] == 0xEF && t.carry[1] == 0xBB && t.carry[2] == 0xBF:
		t.consumeBOM(3)
	case len(t.carry) >= 1 && t.carry[0] != 0xEF:
		t.bomDone = true
	case len(t.carry) >= 2 && (t.carry[1] != 0xBB):
		t.bomDone = true
	}
}

func (t *Transcoder) consumeBOM(n int) {
	if t.cfg.EmitBOM {
		t.emit(0xFEFF)
	}
	t.stats.BOMBytes = int64(n)
	t.carry = t.carry[n:]
	t.bomDone = true
}
