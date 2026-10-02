// Package norm 是流式行尾与空白规范化器：行尾统一为 \n，删除行尾空白，
// 应用末尾换行策略，维护双向偏移映射。单个 Norm 实例不是并发安全的。
package norm

import (
	"errors"
	"fmt"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// 可判定的哨兵错误（包在 ErrAt 里，errors.Is 可区分）。
var (
	ErrNUL    = errors.New("norm: NUL byte in strict mode")
	ErrOutput = errors.New("norm: output exceeds limit")
	ErrClosed = errors.New("norm: write after terminal state")
)

// ErrAt 携带原文偏移的错误。
type ErrAt struct {
	Off int
	Err error
}

func (e *ErrAt) Error() string { return fmt.Sprintf("offset %d: %v", e.Off, e.Err) }
func (e *ErrAt) Unwrap() error { return e.Err }

// Options 配置规范化器；MaxPending/MaxOutput <= 0 表示不限制。
type Options struct {
	Policy     Policy
	Strict     bool
	MaxPending int
	MaxOutput  int
}

// Norm 是流式规范化器。
type Norm struct {
	opt   Options
	sc    eol.Scanner
	wsb   *ws.Buf
	wsOff int
	out   []byte
	m     span.Map
	orig  int
	done  bool
}

// New 创建规范化器。
func New(o Options) *Norm {
	if o.MaxPending <= 0 {
		o.MaxPending = -1
	}
	return &Norm{opt: o, wsb: ws.New(o.MaxPending)}
}

// Write 流式喂入；出错时实例进入终态，已输出内容保留。
func (n *Norm) Write(p []byte) (int, error) {
	if n.done {
		return 0, &ErrAt{n.orig, ErrClosed}
	}
	for i, b := range p {
		if err := n.step(b); err != nil {
			n.done = true
			return i, err
		}
	}
	return len(p), nil
}

func (n *Norm) step(b byte) error {
	off := n.orig
	n.orig++
	if n.opt.Strict && b == 0 {
		return &ErrAt{off, ErrNUL}
	}
	r := n.sc.Feed(b, off)
	if r.End {
		n.resolveWS(false) // 行尾前的空白是行尾空白，删除
	}
	if r.NL {
		if err := n.emit('\n', r.NLOff, r.NLLen); err != nil {
			return err
		}
	}
	if !r.Pass {
		return nil
	}
	if ws.IsWS(b) {
		if n.wsb.Len() == 0 {
			n.wsOff = off
		}
		if err := n.wsb.Add(b); err != nil {
			return &ErrAt{off, err}
		}
		return nil
	}
	if err := n.resolveWS(true); err != nil {
		return err
	}
	return n.emit(b, off, 1)
}

func (n *Norm) emit(b byte, off, olen int) error {
	if n.opt.MaxOutput > 0 && len(n.out) >= n.opt.MaxOutput {
		return &ErrAt{off, ErrOutput}
	}
	n.out = append(n.out, b)
	n.m.Add(off, len(n.out)-1, olen, 1)
	return nil
}

// resolveWS 判定待定空白：emit=true 原样输出（行中），false 删除（行尾/流尾）。
func (n *Norm) resolveWS(emit bool) error {
	l := n.wsb.Len()
	if l == 0 {
		return nil
	}
	defer n.wsb.Reset()
	if !emit {
		n.m.Add(n.wsOff, len(n.out), l, 0)
		return nil
	}
	if n.opt.MaxOutput > 0 && len(n.out)+l > n.opt.MaxOutput {
		return &ErrAt{n.wsOff, ErrOutput}
	}
	n.out = append(n.out, n.wsb.Bytes()...)
	n.m.Add(n.wsOff, len(n.out)-l, l, l)
	return nil
}

// Close 结束流：结算待定状态并应用末尾换行策略。
func (n *Norm) Close() error {
	if n.done {
		return &ErrAt{n.orig, ErrClosed}
	}
	n.done = true
	if nl, off := n.sc.Finish(); nl {
		if err := n.emit('\n', off, 1); err != nil {
			return err
		}
	}
	n.resolveWS(false) // 删除不产出字节，不会超限
	n.out = ApplyPolicy(n.out, &n.m, n.opt.Policy, n.orig)
	return nil
}

func (n *Norm) Output() []byte { return n.out }
func (n *Norm) Map() *span.Map { return &n.m }
