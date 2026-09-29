// Package norm 是带双向偏移映射的流式行尾与空白规范化器。
// 单个 Normalizer 非并发安全；多实例之间互不影响。
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
	EnsureOne               // 非空输出确保恰好一个末尾换行；空仍为空
	TrimBlank               // 删除全部末尾空行但保留一个换行
)

// ErrTerminal 在实例已终态后再次 Write/Close 时返回。
var ErrTerminal = errors.New("norm: writer already closed or in error state")

// ErrorCode 是可判定的错误类别。
type ErrorCode int

const (
	NULByte ErrorCode = iota + 1
	WSBufferLimit
	OutputLimit
)

// Error 带错误码与触发时的原文偏移。
type Error struct {
	Code   ErrorCode
	Offset int
}

func (e *Error) Error() string {
	switch e.Code {
	case NULByte:
		return "norm: NUL byte in strict mode"
	case WSBufferLimit:
		return "norm: trailing-whitespace buffer limit exceeded"
	default:
		return "norm: output size limit exceeded"
	}
}

// Config 配置规范化器；上限为 0 表示不启用。
type Config struct {
	Ending      Ending
	StrictNUL   bool
	MaxPending  int
	MaxOutput   int
	RawSegments bool // par 内部：EOF 不按行尾判定末尾空白
}

// Normalizer 是流式规范化器（非并发安全）。
type Normalizer struct {
	cfg      Config
	det      *eol.Detector
	ws       *ws.Tracker
	mp       *span.Map
	out      []byte
	pend     []byte // 待定空白原文（尚未判定是否行尾）
	orig     int
	dead     bool
	isClosed bool
	term     error
	loneTail bool // 最后一个输出行是否来自单独 \r

	// 供 par 使用的段边界信息
	LeadWS      int  // 段首被判定的空白数
	LeadDel     bool // 段首空白被删（否则原样保留）
	TailWS      int  // 段尾被删待定空白数
	TailCR      bool // 段尾 \n 来自单独 \r
	PendCR      bool // 段尾停在 \r|\n 的待定 \r
	TailPending int  // RawSegments 下段尾未判定空白数
}

// New 创建规范化器。
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, det: eol.New(), ws: ws.New(), mp: span.New()}
}

func (n *Normalizer) fail(code ErrorCode) error {
	e := &Error{Code: code, Offset: n.orig}
	n.term, n.dead = e, true
	return e
}

func (n *Normalizer) emit(b byte) error {
	if lim := n.cfg.MaxOutput; lim > 0 && len(n.out) >= lim {
		return n.fail(OutputLimit)
	}
	n.out = append(n.out, b)
	return nil
}

// Write 喂入一个分块；出错即进入终态，已输出内容保留。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.dead {
		return 0, n.term
	}
	if n.isClosed {
		return 0, ErrTerminal
	}
	for _, b := range p {
		if err := n.feed(b); err != nil {
			return 0, err
		}
		n.orig++
	}
	return len(p), nil
}

// Close 结束流并施加末尾换行策略。
func (n *Normalizer) Close() error {
	if n.dead {
		return n.term
	}
	if n.isClosed {
		return ErrTerminal
	}
	n.isClosed = true
	if n.det.Pending() {
		n.resolvePend(false)
		n.loneTail = true
		n.PendCR = false
		n.line()
	}
	if n.cfg.RawSegments {
		n.TailPending = n.ws.Pending()
		n.TailCR = n.loneTail && len(n.out) > 0 && n.out[len(n.out)-1] == '\n'
		return nil
	}
	{
		if n.ws.Pending() > 0 {
			n.resolvePend(false)
		}
		n.applyEnding()
		n.TailCR = n.loneTail && len(n.out) > 0 && n.out[len(n.out)-1] == '\n'
	}
	return nil
}

func (n *Normalizer) resolvePend(keep bool) {
	cnt := n.ws.Drop()
	if cnt == 0 {
		return
	}
	n.lead(cnt, !keep)
	if keep {
		n.mp.Keep(cnt, cnt)
		n.out = append(n.out, n.pend...)
	} else {
		n.mp.Delete(cnt)
		n.TailWS = cnt
	}
	n.pend = n.pend[:0]
}

func (n *Normalizer) lead(cnt int, del bool) {
	if n.LeadWS != 0 || len(n.out) != 0 || n.mp.OrigLen() != 0 {
		return
	}
	n.LeadWS, n.LeadDel = cnt, del
}

func (n *Normalizer) line() error {
	n.mp.Keep(1, 1)
	n.TailWS = 0
	return n.emit('\n')
}

func (n *Normalizer) feed(b byte) error {
	if b == 0 && n.cfg.StrictNUL {
		return n.fail(NULByte)
	}
	if ws.IsWS(b) {
		if n.cfg.MaxPending > 0 && n.ws.Pending() >= n.cfg.MaxPending {
			return n.fail(WSBufferLimit)
		}
		n.ws.Add()
		n.pend = append(n.pend, b)
		return nil
	}
	for _, a := range n.det.Feed(b) {
		switch a.Kind {
		case eol.ActHold:
			n.PendCR = true
		case eol.ActCRLF:
			n.resolvePend(false)
			n.mp.Delete(1)
			n.PendCR = false
			n.loneTail = false
			if err := n.line(); err != nil {
				return err
			}
		case eol.ActCR:
			n.resolvePend(false)
			n.loneTail = true
			n.PendCR = false
			if err := n.line(); err != nil {
				return err
			}
		case eol.ActLF:
			n.resolvePend(false)
			n.loneTail = false
			if err := n.line(); err != nil {
				return err
			}
		case eol.ActKeep:
			n.resolvePend(true)
			n.mp.Keep(1, 1)
			if err := n.emit(b); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *Normalizer) applyEnding() {
	n.out, n.mp = ApplyEnding(n.mp, n.out, n.cfg.Ending)
}

// Pending 返回 RawSegments 模式下 Close 后仍未判定的空白字节副本。
func (n *Normalizer) Pending() []byte {
	out := make([]byte, len(n.pend))
	copy(out, n.pend)
	return out
}

// ApplyEnding 对已完成基础规范化（Keep 语义）的输出与映射施加末尾策略。
func ApplyEnding(mp *span.Map, out []byte, e Ending) ([]byte, *span.Map) {
	k := 0
	for k < len(out) && out[len(out)-1-k] == '\n' {
		k++
	}
	if e == Keep || len(out) == 0 || (e == TrimBlank && k == 0) {
		return out, mp
	}
	cut := mp.OutLen() - k
	rs := []span.Run{}
	for _, r := range mp.Runs() {
		if r.NP+r.NL <= cut {
			rs = append(rs, r)
			continue
		}
		if r.NP < cut {
			d := cut - r.NP
			if r.OL > 0 {
				r.OL = d
			}
			r.NL = d
			rs = append(rs, r)
		}
	break
	}
	m2 := span.FromRuns(rs)
	m2.Insert(1)
	return append(out[:len(out)-k], '\n'), m2
}

// Output 返回已规范化输出副本。
func (n *Normalizer) Output() []byte {
	out := make([]byte, len(n.out))
	copy(out, n.out)
	return out
}

// Map 返回偏移映射。
func (n *Normalizer) Map() *span.Map { return n.mp }

// Runs 导出当前映射区间（供 par）。
func (n *Normalizer) Runs() []span.Run { return n.mp.Runs() }

// Run 一次性规范化。
func Run(p []byte, cfg Config) ([]byte, *span.Map, error) {
	n := New(cfg)
	if _, err := n.Write(p); err != nil {
		return n.Output(), n.Map(), err
	}
	if err := n.Close(); err != nil {
		return n.Output(), n.Map(), err
	}
	return n.Output(), n.Map(), nil
}
