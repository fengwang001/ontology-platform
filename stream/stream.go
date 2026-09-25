// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transformer 实例不是并发安全的。
package stream

import (
	"errors"
	"fmt"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Dir 是转码方向。
type Dir uint8

const (
	U8toU8 Dir = iota
	U8toU16LE
	U8toU16BE
	U16LEtoU8
	U16BEtoU8
)

var (
	ErrInvalid   = errors.New("stream: invalid byte unit")     // 非法字节单元
	ErrTruncated = errors.New("stream: truncated input")       // 流在字符中间结束
	ErrLimit     = errors.New("stream: output limit exceeded") // 输出超上限
	ErrTerminal  = errors.New("stream: write after terminal")  // 终态后再写
)

// InvalidError 带非法单元在整个输入流中的起始偏移与长度。
type InvalidError struct{ Offset, Length int }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("stream: invalid unit at byte %d length %d", e.Offset, e.Length)
}
func (e *InvalidError) Is(t error) bool { return t == ErrInvalid }

// Config 配置转码器。
type Config struct {
	Dir        Dir
	Strict     bool
	MaxOut     int
	KeepBOM    bool // 保留开头 BOM 为普通 U+FEFF
	EmitBOM    bool // 输出开头写 BOM
	BaseOff    int  // 错误偏移基数（par 段用）
	NoInputBOM bool // 内部段：流不可能再以 BOM 开头
	NoEOF      bool // 内部段：段尾不是真 EOF，残留前缀不算截断
}

// Stats 是字节守恒统计。
type Stats struct {
	Scalars, Invalid, BadBytes, BOMBytes, Consumed int
	LegalBytes                                     int
}

// Transformer 是流式转码器（非并发安全）。
type Transformer struct {
	cfg     Config
	out     []byte
	d8      *u8.Dec
	d16     *u16.Dec
	st      Stats
	err     error
	closed  bool
	headSt  int
	headBuf []byte
}

var u8BOM = [3]byte{0xEF, 0xBB, 0xBF}

// New 创建转码器。
func New(cfg Config) *Transformer {
	t := &Transformer{cfg: cfg}
	if cfg.Dir <= U8toU16BE {
		t.d8 = &u8.Dec{}
	} else {
		def := u16.LE
		if cfg.Dir == U16BEtoU8 {
			def = u16.BE
		}
		t.d16 = u16.NewDec(def)
	}
	if cfg.EmitBOM {
		t.put(scalar.BOM)
	}
	return t
}

func (t *Transformer) put(r rune) {
	if t.cfg.Dir == U8toU8 || t.cfg.Dir >= U16LEtoU8 {
		t.out = u8.EncodeAppend(t.out, r)
		return
	}
	ord := u16.LE
	if t.cfg.Dir == U8toU16BE {
		ord = u16.BE
	}
	t.out = u16.EncodeAppend(t.out, r, ord)
}

func (t *Transformer) rlen(r rune) int {
	if t.cfg.Dir == U8toU8 || t.cfg.Dir >= U16LEtoU8 {
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
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Checks 返回输入字节被检查的总次数。
func (t *Transformer) Checks() int {
	if t.d8 != nil {
		return t.d8.Checks()
	}
	return t.d16.Checks()
}

// Pending 返回尚未定案的暂存字节数。
func (t *Transformer) Pending() int {
	if t.d8 != nil {
		return t.d8.Pending()
	}
	return 0
}

// Output 返回已转码输出。
func (t *Transformer) Output() []byte { return t.out }

// Stats 返回字节守恒统计快照。
func (t *Transformer) Stats() Stats { return t.st }
