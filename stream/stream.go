// Package stream 提供带非法字节替换（U+FFFD）的流式 UTF-8 ⇄ UTF-16
// 转码器：Write / Close / Output。单个 Transcoder 实例非并发安全。
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

// Direction 选择转码方向。
type Direction int

const (
	U8to16 Direction = iota
	U16to8
)

// 四类可判定错误，彼此用 errors.Is 区分。
var (
	ErrInvalidByte = errors.New("stream: invalid byte unit")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrLimit       = errors.New("stream: output limit exceeded")
	ErrClosed      = errors.New("stream: write after terminal state")
)

// UnitError 携带非法单元（或截断点）在整个输入流中的起始字节偏移与长度。
type UnitError struct {
	Kind  error // ErrInvalidByte 或 ErrTruncated
	Off   int64 // 起始字节偏移
	Bytes int   // 与替换模式下该单元吞掉的字节数一致
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

// Config 配置转码器。Limit<=0 表示不限输出字节数。
type Config struct {
	Dir     Direction
	Order   u16.Order
	Strict  bool
	KeepBOM bool
	Limit   int
}

// Stats 是字节守恒统计：ValidBytes+BadBytes+BOMBytes==Consumed。
type Stats struct {
	ValidScalars int64
	BadUnits     int64
	ValidBytes   int64
	BadBytes     int64
	BOMBytes     int64
	Consumed     int64
	Checks       int64 // 字节被检查的总次数（复杂度证据）
}

type entry struct {
	b   byte
	off int64
}

// Transcoder 是流式转码器；非并发安全。
type Transcoder struct {
	cfg      Config
	d8       u8.Decoder
	d16      u16.Decoder
	out      []byte
	st       Stats
	started  bool   // 首个标量是否已产出
	pendOff  int64  // 当前未完成首字节的绝对偏移
	queue    []entry // 待处理（重喂）字节，长度恒 <=3
	terminal error
	closed   bool
	winLo    int64 // par 窗口模式：只产出 Off>=winLo 的单元
	winHi    int64 // par 窗口模式：Off<winHi（flush 后可含截断单元）
	noFlush  bool  // par 非尾段：流末残留不报错
}

// New 构造转码器。
func New(cfg Config) *Transcoder {
	return &Transcoder{winHi: 1<<63 - 1, d16: *u16.NewDecoder(cfg.Order)}
}

// Checks 返回字节被检查总次数。
func (t *Transcoder) Checks() int64 { return t.st.Checks }

// Stat 返回统计快照。
func (t *Transcoder) Stat() Stats { return t.st }

// Output 返回截至目前的输出字节（未拷贝）。
func (t *Transcoder) Output() []byte { return t.out }

func (t *Transcoder) emit(r rune, start, n int64) bool {
	if !t.started {
		t.started = true
		if r == 0xFEFF {
			t.st.BOMBytes += n
			if !t.cfg.KeepBOM {
				return true
			}
		}
	}
	enc := t.encode(r)
	if t.cfg.Limit > 0 && len(t.out)+len(enc) > t.cfg.Limit {
		return false
	}
	t.out = append(t.out, enc...)
	t.st.ValidScalars++
	t.st.ValidBytes += n
	return true
}

func (t *Transcoder) encode(r rune) []byte {
	if t.cfg.Dir == U8to16 {
		return u16.Encode(nil, t.cfg.OrderOrLE(), r)
}
	buf := make([]byte, u8.EncodeLen(r))
	u8.Encode(buf, r)
	return buf
}

func (t *Transcoder) badUnit(start, n int64) error {
	t.st.BadUnits++
	t.st.BadBytes += n
	if t.cfg.Strict {
		return &UnitError{Kind: ErrInvalidByte, Off: start, Bytes: int(n)}
	}
	if !t.emit(0xFFFD, start, n) {
		t.st.BadUnits--
		t.st.BadBytes -= n
		return ErrLimit
	}
	return nil
}
