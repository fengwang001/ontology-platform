// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码。
// 单个 Transcoder 不是并发安全的：同一时刻只能有一个 Write/Close 调用。
package stream

import (
	"ontology/u16"
	"ontology/u8"
)

// Direction 选择转码方向。
type Direction uint8

const (
	U8toU8 Direction = iota
	U8toU16
	U16toU8
)

// Config 配置转码器。
type Config struct {
	Dir       Direction
	Strict    bool       // 严格模式：第一个非法单元即错
	Order     u16.Order  // U16 输入无 BOM 时的默认字节序
	EmitBOM   bool       // 输入开头的 BOM 是否输出到目标
	MaxOutput int        // 输出字节上限；0 表示不限
	headSkip  int        // par 内部：仅驱动状态、不计统计的头部字节数
	noBOM     bool       // par 内部：跳过流首 BOM 处理
}

// Stats 是字节守恒统计。
type Stats struct {
	Valid    int64 // 合法标量数（不含被丢弃的 BOM）
	Illegal  int64 // 非法单元数
	BadBytes int64 // 被非法单元吞掉的字节数
	BOMBytes int64 // BOM 占用的输入字节数
	Consumed int64 // 已提交（与输出对应）的输入字节数
	Cache    int   // 当前切分缓存字节数
	Checks   int64 // 字节被检查的总次数
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg       Config
	dec8      *u8.Decoder
	dec16     *u16.Decoder
	out       []byte
	abs       int64
	order     u16.Order
	odd       byte
	oddSet    bool
	bomSeen   bool
	closed    bool
	term      error
	st        Stats
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, order: cfg.Order}
	if cfg.Dir == U16toU8 {
		t.dec16 = u16.NewDecoder(cfg.Order)
	} else {
		t.dec8 = u8.NewDecoder(nil)
	}
	return t
}

func (t *Transcoder) outLen(r rune) int {
	if t.cfg.Dir == U8toU16 {
		if r > 0xFFFF {
			return 4
		}
		return 2
	}
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// emit 输出一个非 BOM 单元；越限返回 false，输出与统计保持不变。
func (t *Transcoder) emit(r rune, bad bool, inputBytes int) bool {
	if t.cfg.MaxOutput > 0 && len(t.out)+t.outLen(r) > t.cfg.MaxOutput {
		return false
	}
	if t.cfg.Dir == U8toU16 {
		t.out = u16.AppendEncode(t.out, r, t.order)
	} else {
		t.out = u8.AppendEncode(t.out, r)
	}
	t.abs += int64(inputBytes)
	if t.abs > int64(t.cfg.headSkip) {
		if bad {
			t.st.Illegal++
			t.st.BadBytes += int64(inputBytes)
		} else {
			t.st.Valid++
		}
	}
	return true
}

func (t *Transcoder) emitBOM(inputBytes int) bool {
	n := 3
	if t.cfg.Dir == U8toU16 {
		n = 2
	}
	if t.cfg.EmitBOM && t.cfg.MaxOutput > 0 && len(t.out)+n > t.cfg.MaxOutput {
		return false
	}
	if t.cfg.EmitBOM {
		if t.cfg.Dir == U8toU16 {
			t.out = u16.AppendBOM(t.out, t.order)
		} else {
			t.out = u8.AppendEncode(t.out, 0xFEFF)
		}
	}
	t.abs += int64(inputBytes)
	t.st.BOMBytes += int64(inputBytes)
	return true
}

// Stats 返回当前统计快照。
func (t *Transcoder) Stats() Stats {
	s := t.st
	s.Consumed = t.abs
	if t.cfg.Dir == U16toU8 {
		s.Cache = len(t.pendingHead())
	} else {
		s.Cache = len(t.dec8.Pending())
	}
	s.Checks = t.st.Checks
	return s
}

// Output 返回已产生的输出字节（只读视图，Close 前有效）。
func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) pendingHead() []byte {
	if t.oddSet {
		return []byte{t.odd}
	}
	return nil
}
