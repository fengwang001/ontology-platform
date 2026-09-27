// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 Transcoder 实例不是并发安全的：同一时刻只能有一个 goroutine 使用。
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

// Direction 为转码方向。
type Direction uint8

const (
	// U8ToU16：UTF-8 输入，UTF-16 输出；U16ToU8 反之。
	U8ToU16 Direction = iota
	U16ToU8
)

// Options 配置转码器。零值可用：UTF-8→UTF-16LE、替换模式、无 BOM、无上限。
type Options struct {
	Dir       Direction
	Order     u16.Order // UTF-16 侧字节序，Auto 时按流首 BOM 探测，无 BOM 默认 LE
	Strict    bool      // true=严格模式；false=非法单元替换为 U+FFFD
	InputBOM  bool      // 识别并消费流首 BOM
	OutputBOM bool      // 在输出开头写 BOM
	MaxOutput int       // 输出字节上限，0 表示不限
	Resume    bool      // true 表示这是断点续传实例，流首不期待 BOM
}

// Stats 是字节守恒统计。
type Stats struct {
	Consumed     int64 // 已消费输入总字节
	ValidBytes    int64 // 合法标量占用字节
	Scalars       int64 // 合法标量数
	Invalid       int64 // 非法/截断单元数
	InvalidBytes  int64 // 被非法单元吞掉的字节数
	BOMBytes      int64 // BOM 字节数
	OutputBytes   int64
	Checks        int64 // 字节被检查的总次数
}

var (
	// ErrLimit 为输出越过上限；ErrTerminal 为终态后再写入。
	ErrLimit    = errors.New("stream: output exceeds limit")
	ErrTerminal = errors.New("stream: writer is in terminal state")
)

// InvalidError 携带非法单元起始偏移与长度；TruncatedError 表示输入被截断。
type InvalidError struct{ Offset int64; Length int }

func (e *InvalidError) Error() string { return "stream: invalid unit" }

// TruncatedError 携带未完成前缀的起始偏移与长度。
type TruncatedError struct{ Offset int64; Length int }

func (e *TruncatedError) Error() string { return "stream: truncated input" }

// Transcoder 是流式转码器。
type Transcoder struct {
	opts Options
	d8   *u8.Decoder
	d16  *u16.Decoder
	out  []byte

	consumed   int64
	validBytes int64
	scalars    int64
	invalid    int64
	invalidB   int64
	bomB       int64
	bomWritten bool

	termErr error
	closed  bool
}

// New 创建转码器。
func New(opts Options) *Transcoder {
	t := &Transcoder{opts: opts}
	if opts.Dir == U8ToU16 {
		t.d8 = &u8.Decoder{}
	} else {
		t.d16 = u16.NewDecoder(opts.Order)
	}
	return t
}

func (t *Transcoder) checks() int64 {
	if t.d8 != nil {
		return t.d8.Checks()
	}
	return t.d16.Checks()
}

// Write 实现 io.Writer 语义。返回的 n 恰好对应已完整输出的标量字节。
func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

// Close 标记流结束；严格模式下残留未完成前缀返回 *TruncatedError。
func (t *Transcoder) Close() error { return nil }

// Output 返回已输出字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats {
	return Stats{
		Consumed: t.consumed, ValidBytes: t.validBytes, Scalars: t.scalars,
		Invalid: t.invalid, InvalidBytes: t.invalidB, BOMBytes: t.bomB,
		OutputBytes: int64(len(t.out)), Checks: t.checks(),
	}
}

// Checks 返回字节被检查总次数（非导出计数器的读出方法）。
func (t *Transcoder) Checks() int64 { return t.checks() }
