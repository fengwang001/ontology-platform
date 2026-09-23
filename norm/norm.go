// Package norm 是带双向偏移映射的流式行尾与行尾空白规范化器。
// 单个 Normalizer 实例不是并发安全的；多 goroutine 请各自建实例。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Tail 是文件末尾换行策略。
type Tail int

const (
	Keep      Tail = iota // 保留原样
	EnsureOne             // 非空文档末尾恰好一个 \n；空文档为 ""
	TrimEmpty             // 删除末尾真空行（无任何字节的行），末尾保留一个换行
)

var (
	ErrNUL         = errors.New("norm: NUL byte in strict mode")
	ErrWSBuffer    = errors.New("norm: trailing-whitespace buffer limit exceeded")
	ErrOutputLimit = errors.New("norm: output size limit exceeded")
	ErrClosed      = errors.New("norm: write after terminal state")
)

// OffsetError 携带触发错误时的原文偏移。
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config 零值可用：Keep、不严格、无上限。
type Config struct {
	Tail        Tail
	StrictNUL   bool
	WSBufferMax int
	OutputMax   int
}

// Normalizer 是流式状态机。
type Normalizer struct {
	cfg           Config
	eol           eol.Tracker
	wb            ws.Buffer
	b             span.Builder
	out           []byte
	inPos         int
	lineLen       int // 当前行已见非行尾字节数（含待定空白）
	trailingEmpty int // 末尾连续真空行数
	sawAny        bool
	ended         bool
	err           error
}

// New 创建规范化器。
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) fail(err error, off int) error {
	n.ended = true
	n.err = &OffsetError{Err: err, Offset: off}
	return n.err
}

func (n *Normalizer) room(add int) bool {
	return n.cfg.OutputMax == 0 || len(n.out)+add <= n.cfg.OutputMax
}

func (n *Normalizer) noteBreak() {
	if n.lineLen == 0 {
		n.trailingEmpty++
	} else {
		n.trailingEmpty = 0
	}
	n.lineLen = 0
}

// Write 喂入一段原文。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.ended {
		return 0, &OffsetError{Err: ErrClosed, Offset: n.inPos}
	}
	for i, c := range p {
		pos := n.inPos + i
		if n.cfg.StrictNUL && c == 0 {
			return i, n.fail(ErrNUL, pos)
		}
		prev, cur := n.eol.Feed(c)
		if prev == eol.LF { // 挂起 \r 确认为单独行尾
			n.b.Delete(1)
			if !n.room(1) {
				return i, n.fail(ErrOutputLimit, pos)
			}
			n.out = append(n.out, '\n')
			n.b.Insert(1)
			n.noteBreak()
		}
		switch cur {
		case eol.Pending:
			n.wb.Drop() // \r 之前的空白必属行尾
		case eol.CRLF:
			n.b.Delete(1) // \r 销账
			if !n.room(1) {
				return i, n.fail(ErrOutputLimit, pos)
			}
			n.out = append(n.out, '\n')
			n.b.Retain(1) // \n 原样保留
			n.noteBreak()
		case eol.LF:
			n.wb.Drop()
			if !n.room(1) {
				return i, n.fail(ErrOutputLimit, pos)
			}
			n.out = append(n.out, '\n')
			n.b.Insert(1)
			n.noteBreak()
		case eol.None:
			if ws.IsTrailingByte(c) {
				if n.cfg.WSBufferMax > 0 && n.wb.Len() >= n.cfg.WSBufferMax {
					return i, n.fail(ErrWSBuffer, pos)
				}
				n.wb.Add(c)
				n.lineLen++
				n.sawAny = true
			} else {
				if n.wb.Pending() {
					s := n.wb.Take()
					if !n.room(len(s)) {
						return i, n.fail(ErrOutputLimit, pos)
					}
					n.out = append(n.out, s...)
					n.b.Retain(len(s))
				}
				if !n.room(1) {
					return i, n.fail(ErrOutputLimit, pos)
				}
				n.out = append(n.out, c)
				n.b.Retain(1)
				n.lineLen++
				n.sawAny = true
			}
		}
	}
	n.inPos += len(p)
	return len(p), nil
}

// Close 处理流结束并施加末尾策略。
func (n *Normalizer) Close() error {
	if n.ended {
		return n.err
	}
	n.ended = true
	if n.eol.Flush() == eol.LF {
		n.b.Delete(1)
		n.out = append(n.out, '\n')
		n.b.Insert(1)
		n.noteBreak()
	} else {
		n.b.Delete(n.wb.Drop())
	}
	n.applyTail()
	return nil
}

func (n *Normalizer) applyTail() {
	switch n.cfg.Tail {
	case EnsureOne:
		if n.b.OrigLen() == 0 {
			return
		}
		for len(n.out) >= 2 && n.out[len(n.out)-1] == '\n' && n.out[len(n.out)-2] == '\n' {
			n.out = n.out[:len(n.out)-1]
			n.b.Truncate(len(n.out))
		}
		if n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.b.Insert(1)
		}
	case TrimEmpty:
		if !n.sawAny {
			n.out = n.out[:0]
			n.b.Truncate(0)
			return
		}
		for k := 0; k < n.trailingEmpty; k++ {
			n.out = n.out[:len(n.out)-1]
			n.b.Truncate(len(n.out))
		}
		if n.out[len(n.out)-1] != '\n' {
			n.out = append(n.out, '\n')
			n.b.Insert(1)
		}
	}
}

// Output 返回已规范化输出。
func (n *Normalizer) Output() []byte { return n.out }

// Map 冻结并返回双向映射。
func (n *Normalizer) Map() *span.Map { return n.b.Build() }
