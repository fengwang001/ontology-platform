package bencode

import (
	"bytes"
	"sync"
)

// 默认限制。
const (
	// DefaultMaxString 字节串长度默认上限（16 MiB）。
	DefaultMaxString = 16 << 20
	// DefaultMaxDepth 默认嵌套深度上限：顶层列表/字典深度为 1。
	DefaultMaxDepth = 128
)

// Decoder 是 bencode 流式增量解码器。
// Feed、Consumed、Buffered、Err 均可并发调用，
// 结果等价于某个串行顺序。
type Decoder struct {
	mu       sync.Mutex
	maxStr   uint64
	maxDepth int

	consumed int64 // 已完成顶层值消费的字节数
	err      *Error

	pos   int64 // 已处理字节数（下一个待处理字节的绝对偏移）
	st    state
	stack []frame

	// 整数解析状态
	iCanSign bool   // 下一个字节仍允许是负号
	iNeg     bool   // 已见负号
	iDigits  int    // 已读数字个数
	iAcc     uint64 // 已累积的绝对值
	iZero    bool   // 首个数字是 0
	iZeroOff int64  // 该 0 的偏移

	// 字节串解析状态
	sAcc     uint64 // 已累积的长度
	sZero    bool   // 长度首个数字是 0
	sLenOff  int64  // 长度前缀首字节偏移（键错误报告用）
	sLenByte byte   // 长度前缀首字节
	sBuf     []byte // 已收集的内容
	sRemain  uint64 // 剩余内容字节数
}

type state int

const (
	stValue   state = iota // 期待值起始（或容器结束符 e）
	stInt                  // 整数内部
	stStrLen               // 字节串长度内部
	stStrData              // 字节串内容内部
)

// frame 是一个未闭合的列表或字典。
type frame struct {
	dict         bool
	items        []any // 列表元素
	pairs        Dict  // 字典键值对
	expectingKey bool  // 字典：下一项应为键（否则为值）
	pendingKey   []byte
	lastKey      []byte
	hasLastKey   bool
}

// NewDecoder 返回使用默认限制的解码器。
func NewDecoder() *Decoder {
	return NewDecoderWithLimits(DefaultMaxString, DefaultMaxDepth)
}

// NewDecoderWithLimits 返回指定字节串长度上限与嵌套深度上限的解码器。
func NewDecoderWithLimits(maxString uint64, maxDepth int) *Decoder {
	return &Decoder{maxStr: maxString, maxDepth: maxDepth}
}

// Feed 喂入一段字节，返回本次新完成的顶层值。
// 若本次喂入触发规范性违规，仍返回出错点之前已完成的值以及该错误；
// 此后所有 Feed 返回 ErrPoisoned 且不改变任何状态与计数。
func (d *Decoder) Feed(data []byte) ([]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, ErrPoisoned
	}
	var out []any
	for _, b := range data {
		if err := d.step(b, &out); err != nil {
			d.err = err
			return out, err
		}
	}
	return out, nil
}

// step 处理一个字节，d.pos 为该字节的绝对偏移。
func (d *Decoder) step(b byte, out *[]any) *Error {
	off := d.pos
	var err *Error
	switch d.st {
	case stValue:
		err = d.stepValue(b, off, out)
	case stInt:
		err = d.stepInt(b, off, out)
	case stStrLen:
		err = d.stepStrLen(b, off, out)
	case stStrData:
		err = d.stepStrData(b, off, out)
	}
	if err != nil {
		return err
	}
	d.pos++
	return nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func (d *Decoder) top() *frame {
	if len(d.stack) == 0 {
		return nil
	}
	return &d.stack[len(d.stack)-1]
}

// stepValue 处理期待值起始时的字节。
func (d *Decoder) stepValue(b byte, off int64, out *[]any) *Error {
	if f := d.top(); f != nil && f.dict && f.expectingKey {
		// 字典键位置：只接受字节串或结束符。
		switch {
		case b == 'e':
			return d.closeContainer(off, out)
		case isDigit(b):
			return d.beginStrLen(b, off)
		default:
			return &Error{Kind: ErrKeyNotString, Offset: off, Byte: b}
		}
	}
	switch {
	case b == 'i':
		d.st = stInt
		d.iCanSign, d.iNeg = true, false
		d.iDigits, d.iAcc, d.iZero = 0, 0, false
		return nil
	case isDigit(b):
		return d.beginStrLen(b, off)
	case b == 'l':
		return d.openContainer(off, b, false)
	case b == 'd':
		return d.openContainer(off, b, true)
	case b == 'e':
		f := d.top()
		if f == nil || (f.dict && !f.expectingKey) {
			// 顶层出现 e，或字典键后期待值时出现 e。
			return &Error{Kind: ErrIllegalFirstByte, Offset: off, Byte: b}
		}
		return d.closeContainer(off, out)
	default:
		return &Error{Kind: ErrIllegalFirstByte, Offset: off, Byte: b}
	}
}

// stepInt 处理整数内部的一个字节。
func (d *Decoder) stepInt(b byte, off int64, out *[]any) *Error {
	if d.iCanSign {
		d.iCanSign = false
		if b == '-' {
			d.iNeg = true
			return nil
		}
	}
	switch {
	case isDigit(b):
		if d.iZero {
			return &Error{Kind: ErrIntLeadingZero, Offset: off, Byte: b}
		}
		digit := uint64(b - '0')
		limit := uint64(1<<63 - 1)
		if d.iNeg {
			limit = 1 << 63
		}
		if digit > limit || d.iAcc > (limit-digit)/10 {
			return &Error{Kind: ErrIntOverflow, Offset: off, Byte: b}
		}
		d.iAcc = d.iAcc*10 + digit
		d.iDigits++
		if d.iAcc == 0 {
			d.iZero = true
			d.iZeroOff = off
		}
		return nil
	case b == 'e':
		if d.iDigits == 0 {
			// ie、i-e：e 处无数字，属语法错误。
			return &Error{Kind: ErrSyntax, Offset: off, Byte: b}
		}
		if d.iZero && d.iNeg {
			return &Error{Kind: ErrNegativeZero, Offset: d.iZeroOff, Byte: '0'}
		}
		v := int64(d.iAcc)
		if d.iNeg {
			v = int64(-d.iAcc) // 无符号取负，可表示 -2^63
		}
		d.st = stValue
		d.completeValue(v, off, out)
		return nil
	default:
		return &Error{Kind: ErrSyntax, Offset: off, Byte: b}
	}
}

// beginStrLen 以首个长度数字进入字节串长度状态。
func (d *Decoder) beginStrLen(b byte, off int64) *Error {
	d.st = stStrLen
	d.sAcc, d.sZero = 0, false
	d.sLenOff, d.sLenByte = off, b
	return d.accumLen(b, off)
}

// accumLen 累积一个长度数字，并做前导零与上限检查。
func (d *Decoder) accumLen(b byte, off int64) *Error {
	if d.sZero {
		return &Error{Kind: ErrLenLeadingZero, Offset: off, Byte: b}
	}
	digit := uint64(b - '0')
	if digit > d.maxStr || d.sAcc > (d.maxStr-digit)/10 {
		return &Error{Kind: ErrLenTooLarge, Offset: off, Byte: b}
	}
	d.sAcc = d.sAcc*10 + digit
	if d.sAcc == 0 {
		d.sZero = true
	}
	return nil
}

// stepStrLen 处理长度内部的一个字节。
func (d *Decoder) stepStrLen(b byte, off int64, out *[]any) *Error {
	switch {
	case isDigit(b):
		return d.accumLen(b, off)
	case b == ':':
		d.sBuf = make([]byte, 0, min(d.sAcc, 4096))
		d.sRemain = d.sAcc
		if d.sRemain == 0 {
			return d.finishString(off, out)
		}
		d.st = stStrData
		return nil
	default:
		return &Error{Kind: ErrSyntax, Offset: off, Byte: b}
	}
}

// stepStrData 收集内容字节。
func (d *Decoder) stepStrData(b byte, off int64, out *[]any) *Error {
	d.sBuf = append(d.sBuf, b)
	d.sRemain--
	if d.sRemain == 0 {
		return d.finishString(off, out)
	}
	return nil
}

// finishString 完成一个字节串：作为字典键或作为值。
func (d *Decoder) finishString(off int64, out *[]any) *Error {
	s := d.sBuf
	d.sBuf = nil
	d.st = stValue
	if f := d.top(); f != nil && f.dict && f.expectingKey {
		if f.hasLastKey {
			switch c := bytes.Compare(s, f.lastKey); {
			case c == 0:
				return &Error{Kind: ErrDuplicateKey, Offset: d.sLenOff, Byte: d.sLenByte}
			case c < 0:
				return &Error{Kind: ErrKeyOrder, Offset: d.sLenOff, Byte: d.sLenByte}
			}
		}
		f.pendingKey = s
		f.lastKey = s
		f.hasLastKey = true
		f.expectingKey = false
		return nil
	}
	d.completeValue(s, off, out)
	return nil
}

// openContainer 压入一个列表或字典，并检查深度上限。
func (d *Decoder) openContainer(off int64, b byte, dict bool) *Error {
	if len(d.stack) >= d.maxDepth {
		return &Error{Kind: ErrDepthExceeded, Offset: off, Byte: b}
	}
	d.stack = append(d.stack, frame{
		dict:         dict,
		items:        []any{},
		pairs:        Dict{},
		expectingKey: dict,
	})
	return nil
}

// closeContainer 弹出栈顶容器并交付其值。
func (d *Decoder) closeContainer(off int64, out *[]any) *Error {
	f := d.top()
	d.stack = d.stack[:len(d.stack)-1]
	if f.dict {
		d.completeValue(f.pairs, off, out)
	} else {
		d.completeValue(f.items, off, out)
	}
	return nil
}

// completeValue 交付一个完整值：顶层值直接输出，否则挂入父容器。
func (d *Decoder) completeValue(v any, off int64, out *[]any) {
	if len(d.stack) == 0 {
		*out = append(*out, v)
		d.consumed = off + 1
		return
	}
	f := d.top()
	if f.dict {
		f.pairs = append(f.pairs, Pair{Key: f.pendingKey, Value: v})
		f.pendingKey = nil
		f.expectingKey = true
	} else {
		f.items = append(f.items, v)
	}
}

// Consumed 返回已完成顶层值累计消费的字节数。
func (d *Decoder) Consumed() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.consumed
}

// Buffered 返回尚未构成完整顶层值的缓冲字节数。
func (d *Decoder) Buffered() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pos - d.consumed
}

// Err 返回导致粘滞失败态的原始错误，未出错时为 nil。
func (d *Decoder) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		return nil
	}
	return d.err
}
