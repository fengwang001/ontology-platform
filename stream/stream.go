// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码。
// 单个 Transcoder 不是并发安全的：同一时刻只能有一个 goroutine 使用。
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

// Format 是字符编码族。
type Format int

const (
	UTF8 Format = iota
	UTF16
)

// 四类可判定错误：非法字节、输入截断、输出超限、终态后写入。
var (
	ErrLimit   = errors.New("stream: output limit exceeded")
	ErrClosed  = errors.New("stream: write after terminal state")
	ErrInvalid = errors.New("stream: invalid byte unit")
	ErrTrunc   = errors.New("stream: truncated input")
)

// UnitError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Kind   error
	Offset int64
	Len    int
}

func (e *UnitError) Error() string { return e.Kind.Error() }
func (e *UnitError) Unwrap() error { return e.Kind }

// At 便于 errors.Is(err, stream.ErrInvalid)。

// Config 配置转码器。
type Config struct {
	From, To      Format
	Order         u16.Order // UTF-16 字节序；From=UTF16 且开头有 BOM 时 BOM 覆盖它
	Strict        bool
	InputBOMDrop  bool // 丢弃流开头的输入 BOM（默认保留为 U+FEFF）
	OutputBOM     bool // 输出开头写一个 U+FEFF
	MaxOutput     int  // 输出字节上限；0 表示不限
}

// Stats 是字节守恒统计。
type Stats struct {
	Valid       int64 // 已接受的合法标量数
	Invalid     int64 // 非法单元数
	InvalidBytes int64 // 被非法单元吞掉的字节数
	ValidBytes  int64 // 合法标量占用的输入字节数
	BOMBytes    int64 // BOM 占用的输入字节数
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg     Config
	out     []byte
	d8      *u8.Decoder
	d16     *u16.Decoder
	st      Stats
	first   bool // 尚未发出任何单元（用于识别流开头 BOM）
	sniff   []byte
	terminal error
	resume  int
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, first: true}
	if cfg.From == UTF8 {
		t.d8 = &u8.Decoder{}
	} else {
		t.sniff = make([]byte, 0, 2)
	}
	if cfg.OutputBOM {
		t.out = t.emitRune(t.out, u8.RuneError&0 | 0xFEFF)
	}
	return t
}

func (t *Transcoder) emitRune(dst []byte, r rune) []byte {
	if t.cfg.To == UTF8 {
		return u8.Encode(dst, r)
	}
	return u16.Encode(dst, r, t.cfg.Order)
}

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.To == UTF8 {
		return u8.EncodeLen(r)
	}
	return u16.EncodeLen(r)
}

// Write 喂入一段输入。返回 p 中对应到已完整输出单元的字节数；遇上限
// 时该数字即续传断点（详见 DESIGN.md 第 4 节）。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil {
		if errors.Is(t.terminal, ErrLimit) {
			return 0, ErrClosed
		}
		return 0, t.terminal
	}
	if t.cfg.From == UTF16 && t.d16 == nil {
		p = t.sniffBOM(p)
	}
	start := t.consumed()
	t.resume = len(p)
	if t.cfg.From == UTF8 {
		t.d8.Feed(p, t.onUnit)
	} else if t.d16 != nil {
		t.d16.Feed(p, t.onUnit)
	}
	if t.terminal != nil {
		if e, ok := t.terminal.(*UnitError); ok {
			return int(e.Offset - start), e
		}
		return t.resume, ErrLimit
	}
	return len(p), nil
}

func (t *Transcoder) consumed() int64 {
	if t.d8 != nil {
		return t.d8.Consumed()
	}
	if t.d16 != nil {
		return t.d16.Consumed()
	}
	return 0
}

func (t *Transcoder) onUnit(r rune, n int) {
	off := t.consumed() - int64(n)
	if t.first && r == 0xFEFF {
		t.st.BOMBytes += int64(n)
		t.first = false
		if t.cfg.InputBOMDrop {
			return
		}
	} else {
		t.first = false
	}
	if r == u8.RuneError {
		t.st.Invalid++
		t.st.InvalidBytes += int64(n)
		if t.cfg.Strict {
			t.terminal = &UnitError{Kind: ErrInvalid, Offset: off, Len: n}
			return
		}
	} else {
		t.st.Valid++
		t.st.ValidBytes += int64(n)
	}
	if t.cfg.MaxOutput > 0 && len(t.out)+t.encLen(r) > t.cfg.MaxOutput {
		t.terminal = ErrLimit
		t.resume = int(off)
		return
	}
	t.out = t.emitRune(t.out, r)
}

// Close 收尾：残留合法前缀按策略输出 FFFD 或返回截断错误。
func (t *Transcoder) Close() error {
	if t.terminal != nil {
		if errors.Is(t.terminal, ErrClosed) && !t.cfg.Strict {
			return nil
		}
		return t.terminal
	}
	end := func(r rune, n int) {
		if r == u8.RuneError {
			t.st.Invalid++
			t.st.InvalidBytes += int64(n)
			if t.cfg.Strict {
				t.terminal = &UnitError{Kind: ErrTrunc, Offset: t.consumed() - int64(n), Len: n}
				return
			}
			r = u8.RuneError
		}
		t.first = false
		if t.cfg.MaxOutput > 0 && len(t.out)+t.encLen(r) > t.cfg.MaxOutput {
			t.terminal = ErrLimit
			return
		}
		t.out = t.emitRune(t.out, r)
	}
	if t.d8 != nil {
		t.d8.End(end)
	} else if t.d16 != nil {
		t.d16.End(end)
	} else {
		if len(t.sniff) > 0 && t.cfg.Strict {
			t.terminal = &UnitError{Kind: ErrTrunc, Offset: 0, Len: len(t.sniff)}
		}
	}
	if t.terminal != nil {
		return t.terminal
	}
	t.terminal = ErrClosed
	return nil
}

// Output 返回已产生的输出字节（合法终态后内容不再改变）。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.st }

// Checks 返回输入字节被检查的总次数。
func (t *Transcoder) Checks() int64 {
	if t.d8 != nil {
		return t.d8.Checks()
	}
	if t.d16 != nil {
		return t.d16.Checks()
	}
	return 0
}

// MaxPending 是切分缓存硬上限（u8/u16 取大者）。
const MaxPending = u16.MaxPending

func (t *Transcoder) pending() int {
	if t.d8 != nil {
		return t.d8.Pending()
	}
	if t.d16 != nil {
		return t.d16.Pending()
	}
	return len(t.sniff)
}

// Pending 暴露当前切分缓存字节数（供测试断言硬上限）。
func (t *Transcoder) Pending() int { return t.pending() }

func (t *Transcoder) sniffBOM(p []byte) []byte {
	t.sniff = append(t.sniff, p...)
	buf := t.sniff
	if len(buf) < 2 {
		return nil
	}
	order := t.cfg.Order
	consumed := 0
	if len(buf) >= 2 && (buf[0] == 0xFE && buf[1] == 0xFF || buf[0] == 0xFF && buf[1] == 0xFE) {
		if buf[0] == 0xFE {
			order = u16.BE
		} else {
			order = u16.LE
		}
		consumed = 2
	}
	t.d16 = u16.NewDecoder(order)
	rest := buf[consumed:]
	t.sniff = nil
	if consumed == 2 {
		t.st.BOMBytes = 2
		if t.cfg.InputBOMDrop {
			return rest
		}
	}
	if consumed == 0 {
		return rest
	}
	// BOM 保留：作为普通 U+FEFF 发出；BOMBytes 已在上面记过，不再走 onUnit。
	if !t.cfg.InputBOMDrop {
		t.first = false
		t.st.Valid++
		t.st.ValidBytes += 2
		if t.cfg.MaxOutput == 0 || len(t.out)+t.encLen(0xFEFF) <= t.cfg.MaxOutput {
			t.out = t.emitRune(t.out, 0xFEFF)
		}
	}
	return rest
}
