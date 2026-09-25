// Package norm 是流式行尾与空白规范化器：\r\n 与单独的 \r 规范成 \n，删除行尾
// 空白（空格/制表符），应用末尾换行策略，并维护双向偏移映射。单个实例不要求并发安全。
package norm

import (
	"errors"
	"fmt"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// 可判定的哨兵错误，彼此可用 errors.Is 区分；均包装在携带原文偏移的 Error 中。
var (
	ErrNUL    = errors.New("norm: NUL byte in strict mode")
	ErrBuf    = errors.New("norm: whitespace buffer overflow")
	ErrOut    = errors.New("norm: output limit exceeded")
	ErrClosed = errors.New("norm: write after close")
)

type Error struct{ Kind error; Off int } // Off 为原文偏移

func (e *Error) Error() string { return fmt.Sprintf("%v at original offset %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// Policy 是末尾换行策略（推导见 DESIGN.md；均不插入字节）。
type Policy int

const (
	Keep      Policy = iota // 保留原样
	EnsureOne                // 末尾 \n 游程 >=1 时收缩为恰好一个，否则不动
	Strip                    // 删除全部末尾空行；有内容时内容行保留一个换行
)

// Options：MaxBuf/MaxOut 为 0 表示不限；Base 是输入在全局原文中的起始偏移
// （par 分段用）；Open 为 true 时 Close 不结算段尾，待定尾巴由 Tail 交出。
type Options struct {
	Policy         Policy
	Strict, Open   bool
	MaxBuf, MaxOut int
	Base           int
}

type Norm struct {
	opt       Options
	mach      eol.Mach
	pend      ws.Pend
	out       []byte
	mp        span.Map
	pos, tpos int
	done      bool
	err       error
	tail      []byte
}

func New(opt Options) *Norm {
	n := &Norm{opt: opt}
	n.pend.Limit, n.pos = opt.MaxBuf, opt.Base
	return n
}
func (n *Norm) emit(b byte, pos int) error {
	if n.opt.MaxOut > 0 && len(n.out) >= n.opt.MaxOut {
		return &Error{ErrOut, pos}
	}
	n.out = append(n.out, b)
	n.mp.Copy(pos)
	return nil
}

// flushWS 结算待定空白：trailing（行尾或流结束）则删除，否则原样输出。
func (n *Norm) flushWS(trailing bool) error {
	if n.pend.Len() == 0 {
		return nil
	}
	if trailing {
		n.mp.Drop(n.pend.Start(), n.pend.Len())
	} else {
		for i, b := range n.pend.Bytes() {
			if err := n.emit(b, n.pend.Start()+i); err != nil {
				return err
			}
		}
	}
	n.pend.Reset()
	return nil
}
func (n *Norm) step(ev eol.Ev) error {
	if ev.EOL {
		if err := n.flushWS(true); err != nil {
			return err
		}
		if ev.Width == 2 {
			n.mp.Drop(ev.Start, 1) // \r\n 中的 \r
			return n.emit('\n', ev.Start+1)
		}
		return n.emit('\n', ev.Start)
	}
	c := ev.B
	if c == ' ' || c == '\t' {
		if !n.pend.Add(c, ev.Start) {
			return &Error{ErrBuf, ev.Start}
		}
		return nil
	}
	if c == 0 && n.opt.Strict {
		return &Error{ErrNUL, ev.Start}
	}
	if err := n.flushWS(false); err != nil {
		return err
	}
	return n.emit(c, ev.Start)
}

// Write 喂入一段字节，返回已消费字节数；出错后实例进入终态，已输出内容保留。
func (n *Norm) Write(p []byte) (int, error) {
	if n.done {
		if n.err != nil {
			return 0, n.err
		}
		return 0, &Error{ErrClosed, n.pos}
	}
	for i, b := range p {
		pos := n.pos
		n.pos++
		for _, ev := range n.mach.Feed(b, pos) {
			if err := n.step(ev); err != nil {
				n.done, n.err = true, err
				return i, err
			}
		}
	}
	return len(p), nil
}

// Close 结算待定状态并应用末尾换行策略，可重复调用。
func (n *Norm) Close() error {
	if n.done {
		return n.err
	}
	n.done = true
	if n.opt.Open {
		if pos, ok := n.mach.Pending(); ok {
			n.tail, n.tpos = []byte{'\r'}, pos
		} else if n.pend.Len() > 0 {
			n.tail, n.tpos = n.pend.Bytes(), n.pend.Start()
		}
		return nil
	}
	for _, ev := range n.mach.Close() {
		if n.err = n.step(ev); n.err != nil {
			return n.err
		}
	}
	if n.err = n.flushWS(true); n.err != nil {
		return n.err
	}
	n.out = TrimPolicy(n.out, &n.mp, n.opt.Policy)
	return nil
}
func (n *Norm) Output() []byte      { return n.out }           // 规范化输出（不得修改）
func (n *Norm) Map() *span.Map      { return &n.mp }           // 双向偏移映射（不得修改）
func (n *Norm) Tail() ([]byte, int) { return n.tail, n.tpos }  // Open 模式段尾待定字节及原文偏移

// TrimPolicy 在完整输出上应用末尾换行策略并同步映射，返回截断后的输出。
func TrimPolicy(out []byte, mp *span.Map, pol Policy) []byte {
	t := 0
	for t < len(out) && out[len(out)-1-t] == '\n' {
		t++
	}
	d := t - 1
	if pol == Keep || t == 0 {
		d = 0
	} else if pol == Strip && t == len(out) {
		d = t
	}
	if d == 0 {
		return out
	}
	origs := make([]int, d)
	for j := range origs {
		origs[j] = mp.ToOrig(len(out) - d + j)
	}
	mp.Retract(d, origs)
	return out[:len(out)-d]
}
