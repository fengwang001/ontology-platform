// Package norm 是带双向偏移映射的流式行尾与空白规范化器。
//
// 单个 Normalizer 实例不是并发安全的；多个实例可分别在不同 goroutine 使用。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy 是末尾换行策略。
type Policy int

const (
	Keep Policy = iota // 保留原样
	One                // 非空时末尾恰好一个 \n
	Trim               // 删除全部末尾空行；内容无换行则不留 \n
)

var (
	ErrNUL      = errors.New("norm: NUL byte in strict mode")
	ErrWSLimit  = errors.New("norm: trailing-whitespace buffer limit exceeded")
	ErrOutLimit = errors.New("norm: output size limit exceeded")
	ErrClosed   = errors.New("norm: write after terminal state")
)

// OffsetError 携带原文偏移的可判定错误。
type OffsetError struct {
	Err error
	Pos int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Config 配置规范化器。零值表示 Keep、不限制、非严格。
type Config struct {
	Final  Policy
	MaxWS  int  // 待定空白缓冲上限；<=0 不限
	MaxOut int  // 输出字节上限；<=0 不限
	Strict bool // 遇到 NUL 立刻终止
}

// Normalizer 是流式规范化器。零值不可用，必须用 New 构造。
type Normalizer struct {
	cfg     Config
	det     eol.Detector
	tracker ws.Tracker
	b       span.Builder
	out     []byte
	origPos int
	closed  bool
	// 最近一次“内容行”结尾：最后一个含非空白内容的行的行尾后位置。
	contentNL int
	seenNL    bool
}

// New 创建规范化器。
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg} }

func (n *Normalizer) fail(err error, pos int) error {
	n.closed = true
	return &OffsetError{Err: err, Pos: pos}
}

func (n *Normalizer) grow(delta int) error {
	if n.cfg.MaxOut > 0 && len(n.out)+delta > n.cfg.MaxOut {
		return n.fail(ErrOutLimit, n.origPos)
	}
	return nil
}

func (n *Normalizer) emit(b byte) error {
	if err := n.grow(1); err != nil {
		return err
	}
	n.out = append(n.out, b)
	n.b.Copy(1)
	return nil
}

func (n *Normalizer) flushPendingWS() {
	s, e := n.tracker.Range()
	if e > s {
		n.out = append(n.out, n.tracker.Flush()...)
		n.b.Copy(e - s)
	}
	n.tracker.Reset()
}

func (n *Normalizer) dropPendingWS() {
	if n.tracker.Pending() {
		s, e := n.tracker.Drop()
		n.b.Delete(e - s)
		_ = s
	}
}

// loneCR 把一个孤立 \r 定案：删除原 \r，输出一个 \n。
func (n *Normalizer) loneCR(crPos int) error {
	_ = crPos
	n.b.Delete(1)
	return n.emit('\n')
}

// Write 喂入一段字节。终态后返回包装 ErrClosed 的 OffsetError。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, n.fail(ErrClosed, n.origPos)
	}
	for i, c := range p {
		pos := n.origPos
		if n.cfg.Strict && c == 0 {
			n.origPos++
			return i, n.fail(ErrNUL, pos)
		}
		settle, ev, crPos := n.det.Feed(c, pos)
		if settle == eol.CR { // 旧的待定 \r 是孤立行尾
			n.flushPendingWS()
			if err := n.loneCR(crPos); err != nil {
				return i, err
			}
			n.seenNL = true
		}
		var err error
		switch ev {
		case eol.CRLF:
			n.dropPendingWS()
			n.b.Delete(1) // \r
			err = n.emit('\n')
			n.seenNL = true
			n.contentNL = len(n.out)
		case eol.LF:
			n.dropPendingWS()
			err = n.emit('\n')
			n.seenNL = true
			n.contentNL = len(n.out)
		case eol.PendingCR:
			n.flushPendingWS() // \r 前的空白属行内，先冻结
		case eol.Other:
			if ws.IsSpace(c) {
				if n.cfg.MaxWS > 0 && n.tracker.Len() >= n.cfg.MaxWS {
					n.origPos++
					return i, n.fail(ErrWSLimit, pos)
				}
				n.tracker.Add(c, pos)
			} else {
				n.flushPendingWS()
				err = n.emit(c)
			}
		}
		if err != nil {
			n.origPos++
			return i, err
		}
		n.origPos++
	}
	return len(p), nil
}

// Close 定案流结束并应用末尾策略。可重复调用，返回首次结果。
func (n *Normalizer) Close() error {
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if pos, ev := n.det.Close(); ev == eol.CR {
		n.flushPendingWS()
		if err := n.loneCR(pos); err != nil {
			return err
		}
		n.seenNL = true
		n.contentNL = len(n.out)
}
	switch n.cfg.Final {
	case Keep:
		n.flushPendingWS()
	case One:
		n.dropPendingWS()
		if len(n.out) > 0 && n.out[len(n.out)-1] != '\n' {
			if err := n.grow(1); err != nil {
				return err
			}
			n.b.Insert()
			n.out = append(n.out, '\n')
		}
	case Trim:
		n.dropPendingWS()
		cut := n.contentNL
		for cut > 0 && cut < len(n.out) { // contentNL 之后全是空行
			m := n.b.Truncate(cut)
			n.b = span.Builder{}
			n.b.Merge(m, 0, 0)
			n.out = append(n.out[:0:0], n.out[:cut]...)
			break
		}
		if n.seenNL && (len(n.out) == 0 || n.out[len(n.out)-1] != '\n') {
			if err := n.grow(1); err != nil {
				return err
			}
			n.b.Insert()
			n.out = append(n.out, '\n')
		}
	}
	return nil
}

// Output 返回规范化输出（Close 后定型）。
func (n *Normalizer) Output() []byte { return append([]byte(nil), n.out...) }

// Map 返回双向偏移映射（Close 后定型）。
func (n *Normalizer) Map() *span.Map { return n.b.Build() }
