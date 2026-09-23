package stream

import (
	"errors"
	"fmt"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

type Config struct {
	From, To     Encoding
	Strict       bool
	MaxOutput    int // <=0 不限
	EmitBOM      bool
	DropInputBOM bool
}

// Stats 字节守恒：AcceptedBytes+BadBytes+BOMBytes == Consumed。
type Stats struct {
	Accepted      int
	AcceptedBytes int
	BadUnits      int
	BadBytes      int
	BOMBytes      int
	Consumed      int
	Checks        int
}

var (
	ErrInvalidByte = errors.New("stream: invalid byte unit")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrOutputLimit = errors.New("stream: output size limit exceeded")
	ErrClosed      = errors.New("stream: transcoder in terminal state")
)

type InvalidError struct{ Offset, Size int }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("stream: invalid unit offset=%d size=%d", e.Offset, e.Size)
}
func (e *InvalidError) Is(t error) bool { return t == ErrInvalidByte }

type TruncatedError struct{ Offset int }

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("stream: truncated input offset=%d", e.Offset)
}
func (e *TruncatedError) Is(t error) bool { return t == ErrTruncated }

type Transcoder struct {
	cfg      Config
	out      []byte
	s        Stats
	pend     []byte
	phase    int  // u8: 1=多字节; u16: 1=奇数字节 2=等高代理第二代码单元
	want     int  // u8 多字节期望总长
	r        rune // u8 累积值
	big      bool
	orderSet bool
	bomOpen  bool // 仍处在“可能是流首 BOM”的位置
	bomOut   bool
	term     error
}

func New(cfg Config) *Transcoder {
	return &Transcoder{cfg: cfg, big: cfg.From == UTF16BE, bomOpen: true}
}

func (t *Transcoder) Output() []byte {
	out := make([]byte, len(t.out))
	copy(out, t.out)
	return out
}

func (t *Transcoder) Stats() Stats { return t.s }

// CacheLen 为切分缓存当前字节数，硬上限 3。
func (t *Transcoder) CacheLen() int { return len(t.pend) }

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.To == UTF8 {
		return u8.EncodeLen(r)
	}
	if r >= 0x10000 {
		return 4
	}
	return 2
}

func (t *Transcoder) enc(r rune) {
	switch t.cfg.To {
	case UTF8:
		t.out = u8.AppendEncode(t.out, r)
	case UTF16LE:
		t.out = u16.AppendEncodeLE(t.out, r)
	default:
		t.out = u16.AppendEncodeBE(t.out, r)
	}
}

func (t *Transcoder) emit(r rune) bool {
	if t.cfg.EmitBOM && !t.bomOut {
		t.bomOut = true
		if t.cfg.MaxOutput > 0 && len(t.out)+t.encLen(scalar.BOM) > t.cfg.MaxOutput {
			return false
		}
		t.enc(scalar.BOM)
	}
	if t.cfg.MaxOutput > 0 && len(t.out)+t.encLen(r) > t.cfg.MaxOutput {
		return false
	}
	t.enc(r)
	return true
}

// finalize 收尾一个单元；r<0 为非法（size 个字节），bom 为流首输入 BOM。
func (t *Transcoder) finalize(r rune, size int, bom bool) error {
	t.s.Consumed += size
	if bom {
		t.s.BOMBytes += size
		t.bomOpen = false
		return nil
	}
	t.bomOpen = false
	if r < 0 {
		t.s.BadUnits++
		t.s.BadBytes += size
		if t.cfg.Strict {
			return &InvalidError{Offset: t.s.Consumed - size, Size: size}
		}
		if !t.emit(scalar.Replacement) {
			return ErrOutputLimit
		}
		return nil
	}
	if !t.emit(r) {
		return ErrOutputLimit
	}
	t.s.Accepted++
	t.s.AcceptedBytes += size
	return nil
}

func leadLen(b byte) (int, bool) {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2, true
	case b >= 0xE0 && b <= 0xEF:
		return 3, true
	case b >= 0xF0 && b <= 0xF4:
		return 4, true
	default:
		return 0, false
	}
}

func isCont(b byte) bool { return b&0xC0 == 0x80 }

func secondOK(lead, b byte) bool {
	if !isCont(b) {
		return false
	}
	switch {
	case lead == 0xE0:
		return b >= 0xA0
	case lead == 0xED:
		return b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90
	case lead == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

// Close 刷新残留前缀：替换模式输出一个 FFFD，严格模式报截断。
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	if t.cfg.Strict && len(t.pend) > 0 {
		err := &TruncatedError{Offset: t.s.Consumed}
		t.term = err
		return err
	}
	if len(t.pend) > 0 {
		n := len(t.pend)
		t.pend = t.pend[:0]
		if t.phase == 2 {
			// u16 高代理后流结束：仍按 2 字节一个非法单元。
			if err := t.finalize(-1, n, false); err != nil {
				t.term = err
				return err
			}
		} else if err := t.finalize(-1, n, false); err != nil {
			t.term = err
			return err
		}
	}
	t.term = ErrClosed
	return nil
}
