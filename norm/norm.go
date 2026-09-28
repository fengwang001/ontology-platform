// Package norm 是流式行尾与行尾空白规范化器。
// 单个 Normalizer 实例不是并发安全的；并发请为每个 goroutine 各建一个。
package norm

import (
	"errors"

	"ontology/span"
	"ontology/ws"
)
)
)

// Policy 是末尾换行策略。
type Policy int

const (
	Keep Policy = iota // 保留原样
	One                // 有换行则折叠为恰好一个
	Trim               // 有正文则保留一个换行；纯空白/空则删光
)

// Config 配置规范化器。WSLimit/OutLimit <= 0 表示不限。
type Config struct {
	Policy    Policy
	StrictNUL bool
	WSLimit   int
	OutLimit  int
}

// 可判定哨兵错误。
var (
	ErrNUL         = errors.New("norm: NUL byte")
	ErrWSLimit     = ws.ErrLimit
	ErrOutputLimit = errors.New("norm: output size limit exceeded")
	ErrClosed      = errors.New("norm: write after terminal state")
)

// OffsetError 携带触发错误的原文偏移；用 errors.Is 判定具体原因。
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

type engine struct {
	out       []byte
	b         span.Builder
	pend      ws.Pending
	cr        bool
	crPos     int
	consumed  int
	sawBreak  bool
	strict    bool
	wsLimit   int
	outLimit  int
	dead      bool
}

func (g *engine) fail(err error, off int) error {
	g.dead = true
	return &OffsetError{Offset: off, Err: err}
}

func (g *engine) emitNL(off int) error {
	if g.outLimit > 0 && len(g.out) >= g.outLimit {
		return g.fail(ErrOutputLimit, off)
	}
	g.out = append(g.out, '\n')
	g.b.Keep(off, 1)
	g.sawBreak = true
	return nil
}

func (g *engine) emit(p []byte, off int) error {
	if g.outLimit > 0 && len(g.out)+len(p) > g.outLimit {
		return g.fail(ErrOutputLimit, off)
	}
	g.out = append(g.out, p...)
	g.b.Keep(off, len(p))
	return nil
}

func isBreak(b byte) bool { return b == '\n' || b == '\r' }

// step 处理原文偏移 off 处的一个字节。
func (g *engine) step(b byte, off int) error {
	g.b.NoteOrig(off + 1)
	if g.strict && b == 0 {
		return g.fail(ErrNUL, off)
	}
	if ws.IsSpace(b) {
		if !g.pend.Active() {
			g.pend.Begin(off)
		}
		if err := g.pend.Append(b, g.wsLimit); err != nil {
			return g.fail(err, off)
		}
		return nil
	}
	hasWS := g.pend.Active()
	if g.cr {
		g.cr = false
		if b == '\n' && !hasWS { // \r\n 成对：LF 提交，CR 删除
			return g.emitNL(off)
		}
		if err := g.emitNL(g.crPos); err != nil { // 孤立 \r
			return err
		}
		if hasWS {
			if isBreak(b) {
				g.pend.Clear()
			} else if err := g.flushWS(); err != nil {
				return err
			}
		}
		return g.fresh(b, off)
	}
	if hasWS {
		if isBreak(b) {
			g.pend.Clear()
		} else if err := g.flushWS(); err != nil {
			return err
		}
	}
	return g.fresh(b, off)
}

func (g *engine) flushWS() error {
	p := g.pend.Take()
	return g.emit(p, g.pendStart(p))
}
func (g *engine) flushWS() error {
	p, start := g.pend.Take()
	return g.emit(p, start)
}

func (g *engine) fresh(b byte, off int) error {
	switch b {
	case '\n':
		return g.emitNL(off)
	case '\r':
		g.cr, g.crPos = true, off
	default:
		return g.emit([]byte{b}, off)
}
	return nil
}

// Normalizer 流式规范化器；非并发安全。
type Normalizer struct {
	cfg Config
	engine
	closed bool
	final  *span.Map
}

// New 构造规范化器。
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, engine: engine{strict: cfg.StrictNUL, wsLimit: cfg.WSLimit, outLimit: cfg.OutLimit}}
}

// Write 喂入一段原文。错误后实例进入终态。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.dead {
		return 0, ErrClosed
	}
	for _, b := range p {
		if err := n.step(b, n.consumed); err != nil {
			n.consumed++
			return 0, err
		}
		n.consumed++
	}
	return len(p), nil
}

// Close 裁决末尾待定状态并施加末尾策略；幂等可重复调用。
func (n *Normalizer) Close() error {
	if n.closed {
		return ErrClosed
	}
	n.closed = true
	if n.cr {
		if err := n.emitNL(n.crPos); err != nil {
			return err
		}
	}
	n.pend.Clear()
	n.applyPolicy()
	n.final = n.b.Build()
	return nil
}
func (n *Normalizer) applyPolicy() {
	t := 0
	for t < len(n.out) && n.out[len(n.out)-1-t] == '\n' {
		t++
	}
	switch n.cfg.Policy {
	case One:
		if t <= 1 { // 无换行不新增；恰好一个保持
			return
		}
		n.b.TrimOut(len(n.out) - t + 1)
		n.out = n.out[:len(n.out)-t+1]
	case Trim:
		body := len(n.out) - t
		if body == 0 {
			n.b.TrimOut(0)
			n.out = n.out[:0]
			return
		}
		if t <= 1 { // 有正文且已有换行或无换行：保留现状
			return
		}
		orig := n.b.Build().ToOrig(body)
		n.b.TrimOut(body)
		n.b.Keep(orig, 1)
		n.out = append(n.out[:body], '\n')
	}
}

// Output 返回规范化输出（Close 后稳定）。
func (n *Normalizer) Output() []byte { return n.out }

// Map 返回双向偏移映射（Close 后可用）。
func (n *Normalizer) Map() *span.Map { return n.final }

// Normalize 一次性规范化。
func Normalize(data []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(data); err != nil {
		return n.Output(), n.b.Build(), err
	}
	err := n.Close()
	return n.Output(), n.final, err
}

// Frag 是一段原文的片段处理结果：Out/Segs 为已裁决内容，Tail 为
// 留给后继片段共同裁决的原文后缀（至多一个 CR 加一串空白）。
type Frag struct {
	Out      []byte
	Segs     []span.Seg
	Tail     []byte
	SawBreak bool
}

// Fragment 以「后续可能还有字节」的方式处理一段，不做 EOF 裁决与末尾策略。
func Fragment(p []byte, strict bool, wsLimit int) (Frag, error) {
	g := engine{strict: strict, wsLimit: wsLimit}
	for off, b := range p {
		if err := g.step(b, off); err != nil {
			return Frag{}, err
		}
	}
	f := Frag{Out: g.out, Segs: g.b.Segs(), SawBreak: g.sawBreak}
	if g.cr {
		f.Tail = append(f.Tail, '\r')
	}
	if g.pend.Active() {
		f.Tail = append(f.Tail, g.pend.Peek()...)
	}
	return f, nil
}
