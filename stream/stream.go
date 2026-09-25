// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 Transcoder 实例不是并发安全的：同一时刻只能有一个 goroutine 调用其方法。
package stream

import (
	"fmt"

	"ontology/u16"
	"ontology/u8"
)

// Dir 是转码方向。
type Dir int

const (
	// U8ToU16 是 UTF-8 转 UTF-16。
	U8ToU16 Dir = iota
	// U16ToU8 是 UTF-16 转 UTF-8。
	U16ToU8
	// U8ToU8 是清洗模式（UTF-8 → UTF-8，非法转 U+FFFD）。
	U8ToU8
	// U16ToU16 是 UTF-16 → UTF-16 清洗。
	U16ToU16
)

// Config 配置转码器。零值可用：UTF-8→UTF-8、替换模式、无 BOM、无上限。
type Config struct {
	Dir     Dir
	Order   u16.Order // U16 输入/输出字节序；LE 为零值
	Strict  bool      // true：遇非法单元返回错误而非替换
	KeepBOM bool      // true：流首 BOM 作为 U+FEFF 保留；否则丢弃
	MaxOut  int       // 输出字节上限；0 表示不限
	SkipBOM bool      // 内部（par）：即使是流首也不识别 BOM
}

// Stats 是字节守恒统计。
type Stats struct {
	Scalars  int64 // 已接受的合法标量数（不含 BOM 与 FFFD）
	BadUnits int64 // 非法单元数
	BadBytes int64 // 非法单元吞掉的字节数
	BOMBytes int64 // 被识别为流首 BOM 的字节数
	Consumed int64 // 已消费输入字节总数
	Checks   int64 // 字节被检查的总次数（非导出语义，供测试读取）
}

type kind int

const (
	k8in kind = iota
	k16in
)

// Transcoder 是单线程流式转码器。
type Transcoder struct {
	cfg      Config
	in       kind
	out16    bool
	d8       *u8.Decoder
	d16      *u16.Decoder
	out      []byte
	stats    Stats
	closed   bool
	terminal error
	offset   int64 // 已消费到整个输入流的偏移
	bomNeed  int   // 流首 BOM 待匹配字节数（>0 表示匹配中）
	bomGot   []byte
	abs      int64 // 底层状态机已读到的全局字节偏移（含缓存前缀）
	floor    int64 // par：只发射起点 ≥ floor 的单元
	ceiling  int64 // par：单元起点 ≥ ceiling 即暂停（<0 表示不限）
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, ceiling: -1, bomNeed: 3}
	if cfg.Dir == U16ToU8 || cfg.Dir == U16ToU16 {
		t.in, t.d16, t.bomNeed, t.out16 = k16in, u16.NewDecoder(cfg.Order), 2, cfg.Dir == U16ToU16
	} else {
		t.in, t.d8, t.out16 = k8in, &u8.Decoder{}, cfg.Dir == U8ToU16
	}
	if cfg.SkipBOM {
		t.bomNeed = 0
	}
	t.bomGot = make([]byte, 0, 4)
	return t
}

// IllegalError 是非法单元错误（严格模式）。
type IllegalError struct{ Offset, Size int64 }

func (e *IllegalError) Error() string {
	return fmt.Sprintf("illegal unit at offset %d size %d", e.Offset, e.Size)
}

// TruncatedError 是流结束时输入被截断。
type TruncatedError struct{ Offset, Size int64 }

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("truncated input at offset %d size %d", e.Offset, e.Size)
}

// ErrLimit 是输出越过上限；用 errors.Is 判定。
var ErrLimit = fmt.Errorf("output limit exceeded")

// ErrClosed 是终态后再写入。
var ErrClosed = fmt.Errorf("transcoder is in terminal state")

// Checks 返回字节检查总次数。
func (t *Transcoder) Checks() int64 { return t.stats.Checks }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.stats }

// Pending 返回当前切分缓存字节数。
func (t *Transcoder) Pending() int {
	if t.in == k8in {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}

// Output 返回已输出内容（调用方不得修改底层数组）。
func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) emit(r rune) bool {
	var b []byte
	if t.out16 {
		b = u16.Encode(r, t.cfg.Order)
	} else {
		b = u8.Encode(r)
	}
	if t.cfg.MaxOut > 0 && len(t.out)+len(b) > t.cfg.MaxOut {
		return false
	}
	t.out = append(t.out, b...)
	return true
}
