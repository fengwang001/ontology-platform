// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的；同一时刻只允许一个 goroutine 使用。
package stream

import (
	"ontology/u16"
	"ontology/u8"
)

const (
	U8toU16LE = iota
	U8toU16BE
	U16LEtoU8
	U16BEtoU8
	U16AutoToU8 // 仅流开头 BOM 可判定字节序；无 BOM 按 LE
)

// Config 配置转码器；SkipBefore/StopAt 仅供 par 内部分段使用。
type Config struct {
	Dir                int
	Strict             bool
	EmitBOM            bool
	Limit              int
	SkipBefore, StopAt int
}

// Stats 是统计。守恒：合法标量字节数+BadBytes+BOMBytes==Consumed。
type Stats struct {
	Scalars, BadUnits, BadBytes, BOMBytes, Consumed, Checks int64
	MaxPending                                              int
}

// Transcoder 是流式转码器（非并发安全）。
type Transcoder struct {
	cfg                                     Config
	out                                     []byte
	d8                                      u8.Decoder
	d16                                     *u16.Decoder
	autoOrder                               int
	closed                                  bool
	fatal                                   error
	consumed                                int64
	scalars, badUnits, badBytes, bom, check int64
	maxPend                                 int64
	pend                                    []byte
	pendStart                               int64
	rp                                      []byte
	buf                                     [4]byte
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, autoOrder: u16.LE}
	if cfg.Dir == U16LEtoU8 || cfg.Dir == U16AutoToU8 {
		t.d16 = u16.NewDecoder(u16.LE)
	} else if cfg.Dir == U16BEtoU8 {
		t.d16 = u16.NewDecoder(u16.BE)
	}
	return t
}

func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) Stats() Stats {
	return Stats{t.scalars, t.badUnits, t.badBytes, t.bom, t.consumed, t.check, int(t.maxPend)}
}

func (t *Transcoder) order() int {
	if t.cfg.Dir == U8toU16BE || t.cfg.Dir == U16BEtoU8 {
		return u16.BE
	}
	return t.autoOrder
}

// Write 喂入字节；n 仅覆盖本次输入中已彻底结束单元所占字节。残留在切分
// 缓存里的字节不算已消费，由内部保留等下次 Write。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.fatal != nil {
		return 0, t.fatal
	}
	if t.closed {
		t.fatal = ErrClosed
		return 0, ErrClosed
	}
	oldPend, oldStart := len(t.pend), t.pendStart
	i := 0
	var ferr error
	for i < len(p) || len(t.rp) > 0 {
		var b byte
		if len(t.rp) > 0 {
			b, t.rp = t.rp[0], t.rp[1:]
		} else {
			b, i = p[i], i+1
		}
		t.check++
		if err := t.feed(b); err != nil {
			ferr = err
			break
		}
	}
	n := i
	if oldPend > 0 {
		doneOld := int(t.consumed - oldStart)
		if doneOld > oldPend {
			doneOld = oldPend
		}
		if doneOld < 0 {
			doneOld = 0
		}
		n -= doneOld
	}
	if len(t.pend) > 0 && n > 0 {
		fromThis := len(t.pend)
		if oldPend > 0 && t.pendStart < oldStart+int64(oldPend) {
			fromThis = len(t.pend) - oldPend
		}
		if fromThis > n {
			fromThis = n
		}
		if fromThis < 0 {
			fromThis = 0
		}
		n -= fromThis
	}
	if n < 0 {
		n = 0
	}
	return n, ferr
}

// Close 结束流；残留半个字符时替换模式补一个 FFFD，严格模式报截断。
func (t *Transcoder) Close() error {
	if t.fatal != nil {
		return t.fatal
	}
	if t.closed {
		return nil
	}
	t.closed = true
	if len(t.pend) > 0 {
		off := t.pendStart
		if t.cfg.Strict {
			t.fatal = &TruncError{Offset: off}
			return t.fatal
		}
		err := t.commitBad(len(t.pend), off)
		t.pend = t.pend[:0]
		return err
	}
	return nil
}
