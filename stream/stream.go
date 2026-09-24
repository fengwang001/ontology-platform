// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的；并发请用 par 包。
package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

// Encoding 选择编码族。
type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	UTF16 // 仅输入：按流首 BOM 判定，无 BOM 视为 LE
)

// BOMPolicy 决定流首 BOM 的去留。
type BOMPolicy int

const (
	BOMStrip BOMPolicy = iota
	BOMKeep
)

// Stats 是守恒统计。
type Stats struct {
	Scalars    int64
	BadUnits   int64
	BadBytes   int64
	BOMBytes   int64
	Consumed   int64
	ByteChecks int64
}

// Config 配置转码器。
type Config struct {
	From, To Encoding
	Strict   bool
	BOM      BOMPolicy
	MaxOut   int
}

// 哨兵错误，彼此可判定区分。
var (
	ErrBadByte     = errors.New("stream: illegal byte unit")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrOutputLimit = errors.New("stream: output limit exceeded")
	ErrTerminal    = errors.New("stream: write after terminal state")
)

// InputError 携带单元在整个输入流中的起始偏移与长度。
type InputError struct {
	Kind    error // ErrBadByte 或 ErrTruncated
	Off     int64
	Len     int
}

func (e *InputError) Error() string { return e.Kind.Error() }
func (e *InputError) Unwrap() error { return e.Kind }

type ev struct {
	r, from, l int
	ok         bool
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg    Config
	out    []byte
	st     Stats
	err    error
	closed bool
	bomPfx []byte
	bomChecked bool
	order  u16.Order
	d8     *u8.Decoder
	d16    *u16.Decoder
	from16 bool
}

// New 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	t.from16 = cfg.From != UTF8
	if t.from16 {
		t.order = u16.LE
		if cfg.From == UTF16BE {
			t.order = u16.BE
		}
		t.d16 = u16.NewDecoder(t.order)
	} else {
		t.d8 = &u8.Decoder{}
	}
	return t
}

func (t *Transcoder) encode(r rune) ([]byte, bool) {
	if t.cfg.To == UTF8 {
		return u8.Encode(r)
	}
	return u16.Encode(r, t.order)
}

// Write 喂入输入字节；n 仅含已被完整单元消费的字节。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.closed || t.err != nil {
		return 0, ErrTerminal
	}
	n := 0
	for len(p) > 0 {
		// 流开头：按字节前缀探测 BOM；不匹配则立即全部交解码器。
		if !t.bomChecked {
			moved, done := t.bomProbe(&p)
			n += moved
			if done {
				t.bomChecked = true
			} else {
				return n, nil
			}
		}
		if len(p) == 0 {
			break
		}
		e, rest, ok := t.next(p)
		if !ok {
			break
		}
		p = rest
		n += e.from
		t.st.Consumed += int64(e.from)
		if e.ok {
			if err := t.emit(e.r, true); err != nil {
				return n, t.fail(err)
			}
			continue
		}
		t.st.BadUnits++
		t.st.BadBytes += int64(e.l)
		if t.cfg.Strict {
			return n, t.fail(&InputError{Kind: ErrBadByte, Off: t.st.Consumed - int64(e.l), Len: e.l})
		}
		if err := t.emit(u8.Replacement, false); err != nil {
			return n, t.fail(err)
		}
	}
	return n, nil
}

// emit 输出一个标量；scalar=true 时计入合法标量数。
func (t *Transcoder) emit(r rune, scalar bool) error {
	b, ok := t.encode(r)
	if !ok || (t.cfg.MaxOut > 0 && len(t.out)+len(b) > t.cfg.MaxOut) {
		return ErrOutputLimit
	}
	t.out = append(t.out, b...)
	if scalar {
		t.st.Scalars++
	}
	return nil
}

func (t *Transcoder) next(p []byte) (e ev, rest []byte, ok bool) {
	if t.from16 {
		buf := [4]u16.Event{}
		k, r := t.d16.Feed(p, buf[:], &t.st.ByteChecks)
		if k == 0 {
			return e, r, false
		}
		for _, x := range buf[:k] {
			e.from += x.From
		}
		last := buf[k-1]
		return ev{r: int(last.R), from: e.from, l: last.Len, ok: last.OK}, r, true
	}
	buf := [4]u8.Event{}
	k, r := t.d8.Feed(p, buf[:], &t.st.ByteChecks)
	if k == 0 {
		return e, r, false
	}
	for _, x := range buf[:k] {
		e.from += x.From
	}
	last := buf[k-1]
	return ev{r: int(last.R), from: e.from, l: last.Len, ok: last.OK}, r, true
}

func (t *Transcoder) fail(e error) error {
	t.err = e
	return e
}

var (
	bomUTF8 = []byte{0xEF, 0xBB, 0xBF}
	bomLE   = []byte{0xFF, 0xFE}
	bomBE   = []byte{0xFE, 0xFF}
)

// bomProbe 在流开头按字节前缀探测 BOM。
// 返回 moved（本次已结算的输入字节数，计入 n）与 done（探测结束）。
func (t *Transcoder) bomProbe(pp *[]byte) (moved int, done bool) {
	p := *pp
	target := bomUTF8
	from16Auto := t.cfg.From == UTF16
	if t.cfg.From == UTF16LE {
		target = bomLE
	} else if t.cfg.From == UTF16BE {
		target = bomBE
	}
	_ = from16Auto
	for len(p) > 0 {
		t.st.ByteChecks++
		t.bomPfx = append(t.bomPfx, p[0])
		p = p[1:]
		moved++
		pfx := t.bomPfx
		maxLen := len(target)
		if from16Auto {
			maxLen = 2
		}
		if len(pfx) > maxLen {
			// 前缀长于任何 BOM：不可能匹配，整段前缀是普通输入。
			break
		}
		matched := true
		for i := range pfx {
			if pfx[i] != target[i] {
				matched = false
				break
			}
		}
		if from16Auto && !matched {
			if le, be := prefix2(pfx, bomLE), prefix2(pfx, bomBE); le || be {
				matched = true
			}
		}
		if !matched {
			break
		}
		if len(pfx) == len(target) || from16Auto && len(pfx) == 2 {
			// 完整 BOM。
			bl := len(pfx)
			t.st.Consumed += int64(bl)
			t.st.BOMBytes = int64(bl)
			if from16Auto {
				if pfx[0] == 0xFF {
					t.order = u16.LE
				} else {
					t.order = u16.BE
				}
				t.d16.SetOrder(t.order)
			}
			if t.cfg.BOM == BOMKeep {
				if err := t.emit(0xFEFF, true); err != nil {
					t.err = err
				}
			}
			*pp = p
			return moved, true
		}
		// 仍是 BOM 的真前缀：等待更多字节。
		*pp = p
		return 0, false
	}
	// 不匹配：已累积前缀必须作为普通输入重放给解码器。
	t.st.ByteChecks += int64(len(t.bomPfx))
	*pp = append(append([]byte{}, t.bomPfx...), p...)
	return 0, true
}

func prefix2(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Close 结束流，处理残留前缀。
func (t *Transcoder) Close() error {
	if t.closed {
		return t.err
	}
	t.closed = true
	var l int
	if t.from16 {
		if x := t.d16.Flush(); x != nil {
			l = x.Len
		}
	} else if x := t.d8.Flush(); x != nil {
		l = x.Len
	}
	if l == 0 {
		return t.err
	}
	t.st.Consumed += int64(l)
	if t.cfg.Strict {
		return t.fail(&InputError{Kind: ErrTruncated, Off: t.st.Consumed - int64(l), Len: l})
	}
	t.st.BadUnits++
	t.st.BadBytes += int64(l)
	if err := t.emit(u8.Replacement, false); err != nil {
		return t.fail(err)
}
	return t.err
}

// Output 返回已累积输出。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.st }

// PendingLen 返回当前残留字节数（测试用）。
func (t *Transcoder) PendingLen() int {
	if t.from16 {
		return t.d16.PendingLen()
	}
	return t.d8.PendingLen()
}
