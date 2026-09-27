// Package norm 是带双向偏移映射的流式行尾与空白规范化器。
//
// 单实例不要求并发安全。Write/Close 失败后进入终态，再写返回 ErrTerminal。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Ending 为末尾换行策略。
type Ending uint8

const (
	Keep       Ending = iota // 保留原样
	EnsureOne                // 确保恰好一个末尾换行（空输入→"\n"）
	TrimEmpty                // 全空白输入→""，否则恰好一个末尾换行
)

// Config 配置上限与策略；零值表示对应上限不启用。
type Config struct {
	Ending        Ending
	StrictNUL     bool // 遇 NUL 立刻可判定失败
	MaxWhitespace int  // 待定空白缓冲上限（字节）
	MaxOutput     int  // 输出总字节上限
}

// 哨兵错误。
var (
	ErrNUL               = errors.New("norm: NUL byte in strict mode")
	ErrWhitespaceLimit   = errors.New("norm: trailing-whitespace buffer limit exceeded")
	ErrOutputLimit       = errors.New("norm: output size limit exceeded")
	ErrTerminal          = errors.New("norm: write on terminal normalizer")
)

// OffsetError 携带原文偏移的具名错误，可用 errors.As 判定。
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer 是流式规范化器。
type Normalizer struct {
	cfg      Config
	sc       *eol.Scanner
	wt       *ws.Tracker
	body     []byte // 末尾策略应用前的输出
	runs     []span.Run
	origPos  int // 下一个原文字节偏移
	outPos   int // 下一个输出偏移
	terminal bool
	closed   bool
	out      []byte
	mp       *span.Map
}

// New 返回流式规范化器。
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, sc: eol.New(), wt: ws.New()}
}

// Normalize 一次性规范化便捷入口。
func Normalize(src []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if err := n.Write(src); err != nil {
		return nil, nil, err
	}
	if err := n.Close(); err != nil {
		return nil, nil, err
	}
	return n.Output(), n.Map(), nil
}

func (n *Normalizer) fail(err error, off int) error {
	n.terminal = true
	return &OffsetError{Offset: off, Err: err}
}

func (n *Normalizer) emitKept(b byte) {
	n.runs = append(n.runs, span.Run{
		OrigStart: n.origPos, OrigEnd: n.origPos + 1,
		OutStart: n.outPos, OutEnd: n.outPos + 1,
	})
	n.outPos++
	n.body = append(n.body, b)
}

func (n *Normalizer) emitDeleted() {
	n.runs = append(n.runs, span.Run{
		OrigStart: n.origPos, OrigEnd: n.origPos + 1,
		OutStart: n.outPos, OutEnd: n.outPos,
	})
}

// Write 喂入一段原文。严格 NUL、缓冲上限、输出上限在写入边界拒绝并进入终态。
func (n *Normalizer) Write(p []byte) error {
	if n.terminal || n.closed {
		return n.fail(ErrTerminal, n.origPos)
	}
	for _, b := range p {
		off := n.origPos
		n.origPos++
		if n.cfg.StrictNUL && b == 0 {
			return n.fail(ErrNUL, off)
		}
		for _, ev := range n.sc.Feed(b) {
			switch ev.Kind {
			case eol.LF:
				n.wt.LineEnded()
				n.emitKept('\n')
			case eol.CRDeleted:
				n.emitDeleted()
			case eol.CRPending:
				// 待定 \r：不产出区间，等下一字节。
			default:
				if n.cfg.MaxOutput > 0 && n.outPos >= n.cfg.MaxOutput {
					return n.fail(ErrOutputLimit, off)
				}
				for _, we := range n.wt.Feed(ev.Byte) {
					if we.Kind == ws.Whitespace {
						if we.Byte == ev.Byte && n.wt.PendingLen() > 0 &&
							n.cfg.MaxWhitespace > 0 && n.wt.PendingLen() > n.cfg.MaxWhitespace {
							return n.fail(ErrWhitespaceLimit, off)
						}
						// 进入待定缓冲：暂不产出。
					} else {
						n.emitKept(we.Byte)
					}
				}
				// 冲刷事件里的空白此刻在 Pending 中；Feed 非空白时它们已随事件保留。
				for _, w := range n.wt.Pending() {
					_ = w
				}
			}
		}
	}
	return nil
}

// flushPending 把待定空白按“行中保留”冲刷（在非空白字节已处理后不会用到）。
// 当前实现中 ws.Feed 已直接给出全部事件，这里仅保留显式语义入口。

// Close 结束流：判定待定 \r/空白，应用末尾策略，产出最终输出与映射。
func (n *Normalizer) Close() error {
	if n.terminal {
		return ErrTerminal
	}
	if n.closed {
		return nil
	}
	n.closed = true
	// 流结束：待定空白按行尾空白丢弃；待定 \r 是孤立行尾。
	for _, w := range n.wt.Pending() {
		_ = w
		off := n.origPos - n.wt.PendingLen()
		_ = off
	}
	// 直接重放待定空白的删除区间（它们未在 Write 中产出区间）。
	pendingWS := pendingWhitespace(n)
	for range pendingWS {
		n.emitDeleted()
	}
	for range n.sc.Flush() {
		n.emitKept('\n')
	}
	n.finish()
	return nil
}

// Output 返回最终规范化输出（Close 后有效）。
func (n *Normalizer) Output() []byte { return n.out }

// Map 返回最终双向映射（Close 后有效）。
func (n *Normalizer) Map() *span.Map { return n.mp }

func pendingWhitespace(n *Normalizer) []byte { return n.wt.Pending() }
