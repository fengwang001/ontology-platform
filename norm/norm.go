// Package norm 是流式行尾与行尾空白规范化器。
// 单个 Normalizer 实例不是并发安全的；多 goroutine 请各用各的实例。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

type Ending int

const (
	Keep   Ending = iota // 保留原末尾换行数
	Ensure               // 确保恰好一个末尾换行（空输入→"\n"）
	Trim                 // 删除全部末尾空行但保留一个换行（空输入→""）
)

var (
	ErrSpaceLimit = errors.New("norm: trailing-space buffer limit exceeded")
	ErrOutLimit   = errors.New("norm: output size limit exceeded")
	ErrClosed     = errors.New("norm: write after close or fatal error")
)

type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type NULError struct{ Offset int }

func (e *NULError) Error() string { return "norm: NUL byte at offset" }

type Config struct {
	Ending     Ending
	StrictNUL  bool
	SpaceLimit int
	OutLimit   int
}

type Normalizer struct {
	cfg                          Config
	out                          []byte
	sb                           *span.Builder
	run                          *ws.Run
	closed, fatal, inRun, pendCR bool
	origPos, trailLF             int
}

func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg, sb: span.NewBuilder()} }

func (n *Normalizer) oerr(err error) error { return &OffsetError{Err: err, Offset: n.origPos} }

func (n *Normalizer) emit(b byte) error {
	if n.cfg.OutLimit > 0 && len(n.out) >= n.cfg.OutLimit {
		return n.oerr(ErrOutLimit)
	}
	n.out, n.origPos = append(n.out, b), n.origPos+1
	n.sb.Keep(1)
	if b == '\n' {
		n.trailLF++
	} else {
		n.trailLF = 0
	}
	return nil
}

func (n *Normalizer) dropRun() {
	n.sb.Delete(n.run.Len())
	n.origPos, n.inRun = n.origPos+n.run.Len(), false
}

// emitNL 结算一个行尾，先按需要删除 \r\n 的 \r 与行尾待定空白。
func (n *Normalizer) emitNL(seq eol.Seq) error {
	if seq == eol.SeqCRLF {
		n.sb.Delete(1)
		n.origPos++
	}
	if n.inRun {
		n.dropRun()
	}
	n.pendCR = false
	return n.emit('\n')
}

func (n *Normalizer) step(b byte) error {
	switch {
	case ws.IsSpace(b):
		if !n.inRun {
			n.run, n.inRun = ws.NewRun(n.origPos), true
		}
		n.run.Add(b)
		if n.cfg.SpaceLimit > 0 && n.run.Len() > n.cfg.SpaceLimit {
			return n.oerr(ErrSpaceLimit)
		}
	case b == '\r':
		if n.inRun {
			if err := n.emitNL(eol.SeqCR); err != nil {
				return err
			}
		}
		n.pendCR = true
	case b == '\n':
		return n.emitNL(eol.SeqLF)
	default:
		if n.inRun {
			bs := n.run.Bytes()
			n.inRun = false
			for i := range bs {
				if err := n.emit(bs[i]); err != nil {
					return err
				}
			}
		}
		return n.emit(b)
	}
	return nil
}

func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed {
		return 0, ErrClosed
	}
	for k, b := range p {
		if b == 0 && n.cfg.StrictNUL {
			n.closed, n.fatal = true, true
			return k, &NULError{Offset: n.origPos}
		}
		err := n.step(b)
		if n.pendCR { // step 内部的 \r 分支在 emitNL 后重新置位，属正常
		}
		if err != nil {
			n.closed, n.fatal = true, true
			return k, err
		}
	}
	return len(p), nil
}

func (n *Normalizer) Close() error {
	if n.fatal {
		return ErrClosed
	}
	if n.closed {
		return nil
	}
	n.closed = true
	if n.pendCR {
		if err := n.emitNL(eol.Flush(true)); err != nil {
			n.fatal = true
			return err
		}
	}
	if n.inRun {
		n.dropRun()
	}
	if n.cfg.Ending == Ensure || (n.cfg.Ending == Trim && len(n.out) > 0) {
		n.foldTail()
	}
	if n.cfg.OutLimit > 0 && len(n.out) > n.cfg.OutLimit {
		n.fatal = true
		return n.oerr(ErrOutLimit)
	}
	return nil
}

// foldTail 确保输出以恰好一个 \n 结尾：折叠多余换行，或补一个。
func (n *Normalizer) foldTail() {
	if n.trailLF > 1 {
		n.sb.TrimOutput(n.trailLF - 1)
		n.out = n.out[:len(n.out)-n.trailLF+1]
		n.trailLF = 1
	} else if n.trailLF == 0 {
		n.sb.Insert()
		n.out = append(n.out, '\n')
		n.trailLF = 1
	}
}

func (n *Normalizer) Output() []byte { return n.out }
func (n *Normalizer) Map() *span.Map { return n.sb.Build() }

func Bytes(in []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(in); err != nil {
		return n.Output(), n.Map(), err
	}
	err := n.Close()
	return n.Output(), n.Map(), err
}
