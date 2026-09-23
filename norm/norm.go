// Package norm 是流式行尾与空白规范化器，串联 eol/ws/span，
// 提供末尾换行策略、缓冲/输出上限、严格 NUL 检测与双向偏移映射。
// 单个 Normalizer 不要求并发安全。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Policy int

const (
	Keep Policy = iota // 保留原样
	One                // 有内容则末尾恰好一个 \n（空输入仍为 ""）
	Trim               // 删除全部末尾空行；内容非空则保留一个 \n
)

type Config struct {
	Trailing     Policy
	StrictNUL    bool
	PendingLimit int   // 待定空白串上限，<=0 不限
	OutputLimit  int64 // 输出总字节上限，<=0 不限
}

var (
	ErrClosed      = errors.New("norm: writer closed")
	ErrOutputLimit = errors.New("norm: output exceeds limit")
)

type NULError struct{ OrigOffset int64 }

func (e *NULError) Error() string { return "norm: NUL byte at input offset" }

type LimitError struct {
	OrigOffset int64
	err        error
}

func (e *LimitError) Error() string { return e.err.Error() }
func (e *LimitError) Unwrap() error { return e.err }

type Normalizer struct {
	cfg                   Config
	ed                    eol.Decoder
	wd                    *ws.Decoder
	b                     span.Builder
	out                   []byte
	pos, outPos           int64
	closed, failed, anyNL bool
	anyNon                bool
}

func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg, wd: ws.New(cfg.PendingLimit)} }

func (n *Normalizer) errf(i int, err error) (int, error) {
	n.failed = true
	return i, &LimitError{OrigOffset: n.pos, err: err}
}

func (n *Normalizer) emit(b byte) error {
	if n.cfg.OutputLimit > 0 && n.outPos+1 > n.cfg.OutputLimit {
		return ErrOutputLimit
	}
	n.out, n.outPos = append(n.out, b), n.outPos+1
	return nil
}

// lineEnd 在原文换行位置 orig 输出 \n；delCR 表示属于 \r\n，先登记
// 待定空白与 \r 的删除区间（按顺序登记以保持区间有序）。
func (n *Normalizer) lineEnd(orig int64, delCR bool) error {
	if c := n.wd.Trim(); c > 0 {
		start := orig - int64(c)
		if delCR {
			start--
		}
		n.b.Append(span.Entry{Orig: start, OrigLen: int64(c)})
	}
	if delCR {
		n.b.Append(span.Entry{Orig: orig - 1, OrigLen: 1})
	}
	if err := n.emit('\n'); err != nil {
		return err
	}
	n.b.Append(span.Entry{Orig: orig, Out: n.outPos - 1, OrigLen: 1, OutLen: 1})
	n.anyNL = true
	return nil
}

func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.failed {
		return 0, ErrClosed
	}
	for i := 0; i < len(p); i++ {
		b := p[i]
		if b == 0 && n.cfg.StrictNUL {
			n.failed = true
			return i, &NULError{OrigOffset: n.pos}
		}
		priorCR, k := n.ed.Feed(b)
		if priorCR && n.lineEnd(n.pos-1, false) != nil {
			return n.errf(i, ErrOutputLimit)
		}
		var err error
		switch {
		case k == eol.CRLF2:
			err = n.lineEnd(n.pos, true)
		case k == eol.LF:
			err = n.lineEnd(n.pos, false)
		case b == '\r':
			// 待定 \r 可能与下一段的 \n 配对，暂不输出。
		case ws.IsSpace(b):
			err = n.wd.AddSpace()
		default:
			if l := n.wd.Keep(); l > 0 {
				n.b.Append(span.Entry{Orig: n.pos - int64(l), Out: n.outPos, OrigLen: int64(l), OutLen: int64(l)})
			}
			if err = n.emit(b); err == nil {
				n.b.Append(span.Entry{Orig: n.pos, Out: n.outPos - 1, OrigLen: 1, OutLen: 1})
				n.anyNon = true
			}
		}
		if err != nil {
			return n.errf(i, err)
		}
		n.pos++
	}
	return len(p), nil
}

func (n *Normalizer) Close() error {
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if n.ed.PendingCR() {
		n.ed.Flush()
		if err := n.lineEnd(n.pos-1, false); err != nil {
			n.failed = true
			return &LimitError{OrigOffset: n.pos, err: err}
		}
	}
	if l := n.wd.Trim(); l > 0 {
		n.b.Append(span.Entry{Orig: n.pos - int64(l), OrigLen: int64(l)})
	}
	if n.anyNL {
		switch n.cfg.Trailing {
		case One:
			if err := n.ensureOne(); err != nil {
				n.failed = true
				return &LimitError{OrigOffset: n.pos, err: err}
			}
		case Trim:
			n.cut(n.trimCut())
		}
	}
	return nil
}

func (n *Normalizer) trailingNLs() int64 {
	r := int64(0)
	for r < n.outPos && n.out[n.outPos-1-r] == '\n' {
		r++
	}
	return r
}

func (n *Normalizer) ensureOne() error {
	r := n.trailingNLs()
	switch {
	case r == 1:
	case r > 1:
		n.cut(n.outPos - r + 1)
	default: // 合成换行：纯插入区间，回映到原文终点
		if err := n.emit('\n'); err != nil {
			return err
		}
		n.b.Append(span.Entry{Orig: n.pos, Out: n.outPos - 1, OutLen: 1})
	}
	return nil
}

func (n *Normalizer) trimCut() int64 {
	cut := n.outPos - n.trailingNLs()
	if n.anyNon {
		cut++
	}
	return cut
}

func (n *Normalizer) cut(c int64) {
	n.pos = n.b.Cut(c)
	n.out, n.outPos = n.out[:c], c
}

func (n *Normalizer) Output() []byte        { return n.out }
func (n *Normalizer) Map() *span.Map        { return n.b.Build() }
func (n *Normalizer) Entries() []span.Entry { return n.b.Entries() }
