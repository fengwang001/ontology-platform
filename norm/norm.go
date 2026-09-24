// Package norm 是带双向偏移映射的流式行尾与空白规范化器。
// 单个 Normalizer 不是并发安全的；多个实例可并发使用。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Ending 是文件末尾换行策略。
type Ending int

const (
	Keep      Ending = iota // 保留原样
	EnsureOne               // 非空且无尾换行时补一个；空输入保持空
	Trim                    // 删除全部末尾空行后保留恰好一个换行（空输入保持空）
)

// Config 配置规范化器。零值可用：Keep、不严格、不限缓冲、不限输出。
type Config struct {
	Ending            Ending
	StrictNUL         bool
	MaxTrailingSpaces int // <=0 表示不限
	MaxOutput         int // <=0 表示不限
}

// Result 是一次规范化的输出与映射。
type Result struct {
	Output []byte
	Map    span.Map
}

var (
	// ErrClosed 表示终态后继续写入或重复 Close。
	ErrClosed = errors.New("norm: writer already closed")
)

// OffsetError 是带原文偏移的可判定错误基类。
type OffsetError struct {
	Kind string
	Orig int
}

func (e *OffsetError) Error() string { return "norm: " + e.Kind + " at orig " + itoa(e.Orig) }

// IsNUL 报告是否为严格模式下遇到 NUL 的错误。
func IsNUL(err error) bool { return kind(err) == "nul byte" }

// IsSpaceLimit 报告是否为行尾空白缓冲超限错误。
func IsSpaceLimit(err error) bool { return kind(err) == "trailing space limit" }

// IsOutputLimit 报告是否为输出字节上限错误。
func IsOutputLimit(err error) bool { return kind(err) == "output limit" }

func kind(err error) string {
	var e *OffsetError
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Normalizer 是流式规范化器。
type Normalizer struct {
	cfg     Config
	dec     eol.Decoder
	run     ws.Run
	sb      span.Builder
	out     []byte
	origPos int // 已消费原文长度
	wsStart int // 当前空白运行的原文起点
	wsEnd   int
	closed  bool
}

// New 创建规范化器。
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) fail(kind string) error {
	n.closed = true
	return &OffsetError{Kind: kind, Orig: n.origPos}
}

func (n *Normalizer) emit(b byte) bool {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return false
	}
	n.out = append(n.out, b)
	return true
}

func (n *Normalizer) flushWS(keep bool) {
	if !n.run.Active() {
		return
	}
	if keep {
		b := n.run.Keep()
		if n.cfg.MaxOutput > 0 && len(n.out)+len(b) > n.cfg.MaxOutput {
			n.closed = true
			return
		}
		n.out = append(n.out, b...)
		return
	}
	n.sb.Delete(n.wsStart, n.wsEnd, len(n.out))
	n.run.Drop()
}

func (n *Normalizer) lineEnd() bool {
	n.flushWS(false)
	if !n.emit('\n') {
		n.closed = true
		return false
	}
	return true
}

// Write 送入一个原文分片。任意切分方式结果一致。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, ErrClosed
	}
	for j, b := range p {
		// 上一个 \r 必须先于本字节判定：其后的空白属于新行。
		if n.dec.Pending() && b != '\n' {
			n.dec.Push(eol.Other)
			if !n.lineEnd() {
				return j, &OffsetError{Kind: "output limit", Orig: n.origPos}
			}
		}
		if n.cfg.StrictNUL && b == 0 {
			return j, n.fail("nul byte")
		}
		switch eol.Classify(b) {
		case eol.CR:
			n.dec.Push(eol.CR)
		case eol.LF:
			if n.dec.Pending() {
				n.dec.Push(eol.LF)
				n.sb.Delete(n.origPos-1, n.origPos, len(n.out)) // \r\n 的 \r
			} else {
				n.dec.Push(eol.LF)
			}
			if !n.lineEnd() {
				return j, &OffsetError{Kind: "output limit", Orig: n.origPos}
			}
		default:
			if ws.IsSpace(b) {
				if !n.run.Active() {
					n.wsStart = n.origPos
				}
				n.run.Add(b)
				n.wsEnd = n.origPos + 1
				if n.cfg.MaxTrailingSpaces > 0 && n.run.Len() > n.cfg.MaxTrailingSpaces {
					return j, n.fail("trailing space limit")
				}
			} else {
				n.flushWS(true)
				if n.closed {
					return j, &OffsetError{Kind: "output limit", Orig: n.origPos}
				}
				if !n.emit(b) {
					n.closed = true
					return j, &OffsetError{Kind: "output limit", Orig: n.origPos}
				}
			}
		}
		n.origPos++
	}
	return len(p), nil
}

// Close 结束输入并应用末尾策略；可判定错误时保留已输出内容并进入终态。
func (n *Normalizer) Close() (Result, error) {
	if n.closed {
		return Result{Output: n.out, Map: n.sb.Build()}, ErrClosed
	}
	n.closed = true
	if n.dec.Pending() {
		n.dec.Flush()
		if !n.lineEnd() {
			return Result{Output: n.out, Map: n.sb.Build()}, &OffsetError{Kind: "output limit", Orig: n.origPos}
		}
	}
	n.flushWS(false)
	switch n.cfg.Ending {
	case EnsureOne:
		if n.origPos > 0 {
			k := len(n.out)
			for k > 0 && n.out[k-1] == '\n' {
				k--
			}
			if len(n.out)-k > 1 {
				// 多余的末尾换行删除；末尾换行都在原文末尾，删除起点按输出反查。
				n.sb.Delete(n.origPos-(len(n.out)-k-1), n.origPos-1, k+1)
			}
			n.out = append(n.out[:k], '\n')
			if k == len(n.out) {
				n.sb.Insert(n.origPos, k, 1)
			}
		}
	case Trim:
		core := n.origPos > 0
		k := len(n.out)
		for k > 0 && n.out[k-1] == '\n' {
			k--
		}
		if core {
			n.out = append(n.out[:k], '\n')
		} else {
			n.out = n.out[:k]
		}
	}
	return Result{Output: n.out, Map: n.sb.Build()}, nil
}

func (n *Normalizer) lastByte() byte {
	if len(n.out) == 0 {
		return 0
	}
	return n.out[len(n.out)-1]
}
