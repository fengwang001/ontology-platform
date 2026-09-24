// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 Transcoder 实例不是并发安全的；并行转码见 par 包。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// Enc 是一种字符编码。
type Enc uint8

const (
	UTF8 Enc = iota
	UTF16LE
	UTF16BE
)

// 四类可判定错误，彼此可用 errors.Is 区分。
var (
	ErrIllegal   = errors.New("illegal byte sequence")
	ErrTruncated = errors.New("input truncated")
	ErrLimit     = errors.New("output limit exceeded")
	ErrClosed    = errors.New("transcoder already in terminal state")
)

// UnitError 带非法/截断单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Op     error // ErrIllegal 或 ErrTruncated；ErrLimit 时不带偏移
	Offset int64
	Length int
}

func (e *UnitError) Error() string { return e.Op.Error() }
func (e *UnitError) Unwrap() error { return e.Op }

// Config 配置转码器。Limit<=0 不限；Resume 跳过流首 BOM 识别（断点续传）。
type Config struct {
	From, To Enc
	Strict   bool
	EmitBOM  bool
	Limit    int
	Resume   bool
}

// Stats 满足 AcceptedBytes+BadBytes+BOMBytes == Consumed。
type Stats struct {
	Accepted      int64 // 已接受的合法标量数（不含 BOM 与替换符）
	Bad           int64 // 非法单元数
	AcceptedBytes int64
	BadBytes      int64
	BOMBytes      int64
	Checks        int64 // 字节被检查的总次数
}

// Consumed 是已消费的输入总字节数。
func (s Stats) Consumed() int64 { return s.AcceptedBytes + s.BadBytes + s.BOMBytes }

type Transcoder struct {
	cfg      Config
	out      []byte
	d8       u8.Decoder
	d16      *u16.Decoder
	stats    Stats
	pos      int64 // 已进入 Write 循环的输入字节总数
	closed   int64 // 已闭合（消费）字节数
	terminal error
	ord      u16.Order
	sniff    [2]byte
	sniffN   int
	sniffing bool
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg, ord: u16.LE}
	if cfg.From == UTF16BE {
		t.ord = u16.BE
	}
	if cfg.From != UTF8 && !cfg.Resume {
		t.sniffing = true
	}
	if cfg.From != UTF8 {
		t.d16 = u16.New(t.ord)
	}
	return t
}

func (t *Transcoder) encode(r rune) []byte {
	if t.cfg.To == UTF8 {
		return u8.Encode(r)
	}
	o := u16.LE
	if t.cfg.To == UTF16BE {
		o = u16.BE
	}
	return u16.Encode(r, o)
}

// emit 返回 false 表示越过输出上限（此时该标量尚未写入）。
func (t *Transcoder) emit(r rune) bool {
	b := t.encode(r)
	if t.cfg.Limit > 0 && len(t.out)+len(b) > t.cfg.Limit {
		return false
	}
	t.out = append(t.out, b...)
	return true
}

// Write 返回本批已闭合并已输出单元所占字节数；缓存中的半个字符不计。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil {
		return 0, t.terminal
	}
	startClosed := t.closed
	for i := 0; i < len(p); i++ {
		t.stats.Checks++
		pos := t.pos
		t.pos++
		var err error
		if t.cfg.From == UTF8 {
			err = t.from8(p[i], pos)
		} else {
			err = t.from16(p[i], pos)
		}
		if err != nil {
			t.terminal = err
			return int(t.closed - startClosed), err
		}
	}
	return int(t.closed - startClosed), nil
}

func (t *Transcoder) from8(b byte, pos int64) error {
	for _, ev := range t.d8.Step(b) {
		off := pos + 1 - int64(ev.Len)
		if ev.Kind == u8.Scalar {
			if off == 0 && !t.cfg.Resume && ev.R == scalar.BOM {
				t.stats.BOMBytes += int64(ev.Len)
				t.closed += int64(ev.Len)
				if t.cfg.EmitBOM && !t.emit(scalar.BOM) {
					return &UnitError{Op: ErrLimit}
				}
				continue
			}
			if !t.emit(ev.R) {
				return &UnitError{Op: ErrLimit}
			}
			t.stats.Accepted++
			t.stats.AcceptedBytes += int64(ev.Len)
			t.closed += int64(ev.Len)
		} else {
			if err := t.illegal(off, ev.Len); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Transcoder) illegal(off int64, n int) error {
	t.stats.Bad++
	t.stats.BadBytes += int64(n)
	t.closed += int64(n)
	if t.cfg.Strict {
		return &UnitError{Op: ErrIllegal, Offset: off, Length: n}
	}
	if !t.emit(scalar.Replacement) {
		return &UnitError{Op: ErrLimit}
	}
	return nil
}

func (t *Transcoder) from16(b byte, pos int64) error {
	if t.sniffing {
		t.sniff[t.sniffN] = b
		t.sniffN++
		if t.sniffN < 2 {
			return nil
		}
		t.sniffing = false
		u := uint16(t.sniff[0])<<8 | uint16(t.sniff[1])
		if u == 0xFEFF {
			t.ord, t.d16 = u16.BE, u16.New(u16.BE)
			t.stats.BOMBytes += 2
			t.closed += 2
			if t.cfg.EmitBOM {
				t.emit(scalar.BOM)
			}
			return nil
		}
		if u == 0xFFFE {
			t.ord, t.d16 = u16.LE, u16.New(u16.LE)
			t.stats.BOMBytes += 2
			t.closed += 2
			if t.cfg.EmitBOM {
				t.emit(scalar.BOM)
			}
			return nil
		}
		t.d16 = u16.New(t.ord)
		for j, x := range t.sniff[:2] {
			if err := t.feed16(x, pos-1+int64(j)); err != nil {
				return err
			}
		}
		return nil
	}
	return t.feed16(b, pos)
}

func (t *Transcoder) feed16(b byte, pos int64) error {
	for _, ev := range t.d16.Step(b) {
		off := pos + 1 - int64(ev.Bytes)
		if ev.Kind == u16.Scalar {
			if !t.emit(ev.R) {
				return &UnitError{Op: ErrLimit}
			}
			t.stats.Accepted++
			t.stats.AcceptedBytes += int64(ev.Bytes)
			t.closed += int64(ev.Bytes)
		} else {
			if err := t.illegal(off, 2); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close 结束流；残留半个字符在严格模式返回可区分的截断错误。
func (t *Transcoder) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	var off int64
	var n int
	if t.cfg.From == UTF8 {
		if ev := t.d8.Flush(); ev != nil {
			n = ev.Len
			off = t.pos - int64(n)
		}
	} else {
		half, lone := t.d16.Flush()
		if half {
			n++
		}
		if lone {
			n += 2
		}
		off = t.pos - int64(n)
	}
	if n == 0 {
		t.terminal = ErrClosed
		return nil
	}
	if t.cfg.Strict {
		t.terminal = &UnitError{Op: ErrTruncated, Offset: off, Length: n}
		return t.terminal
	}
	t.emit(scalar.Replacement)
	t.stats.Bad++
	t.stats.BadBytes += int64(n)
	t.terminal = ErrClosed
	return nil
}

// Output 返回已输出字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.stats }
