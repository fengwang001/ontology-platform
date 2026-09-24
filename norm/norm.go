// Package norm 是带双向偏移映射的流式行尾与空白规范化器。
// 单个 Normalizer 实例不是并发安全的；多个实例之间互不影响。
package norm

import (
	"errors"

	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// Policy 决定文件末尾换行如何处理。
type Policy uint8

const (
	Keep       Policy = iota // 保留原样
	ExactlyOne               // 确保至多一个：有则压成一个，空/无则不补
	TrimEmpty                // 删除全部末尾空行但保留一个换行
)

// Config 配置上限；零值的数值上限表示不限。
type Config struct {
	Policy     Policy
	StrictNUL  bool // 遇 NUL 立即报错
	MaxPending int  // 待定空白缓冲上限
	MaxOutput  int  // 输出总字节上限
}

var (
	// ErrClosed 终态后继续写入。
	ErrClosed = errors.New("norm: write after close")
	// ErrNUL 严格模式下遇到 NUL。
	ErrNUL = errors.New("norm: NUL byte")
	// ErrPendingLimit 待定空白超过上限。
	ErrPendingLimit = errors.New("norm: pending whitespace limit")
	// ErrOutputLimit 输出超过总字节上限。
	ErrOutputLimit = errors.New("norm: output limit")
)

// OffsetError 携带触发错误时的原文字节偏移。
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Normalizer 是流式规范化器。
type Normalizer struct {
	cfg     Config
	dec     eol.Decoder
	track   ws.Tracker
	out     []byte
	mp      span.Builder
	pendBuf []byte
	// 尚未建区间的连续原文起点：runOrig/runOut 为当前开放拷贝区间。
	runOrig, runOut, runLen int
	consumed                int
	closed                  bool
	terminal                error
}

// New 创建规范化器。
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg, runOrig: -1} }

func (n *Normalizer) flushRun(o int) {
	if n.runLen > 0 {
		n.mp.Add(span.Run{OrigStart: n.runOrig, OrigLen: n.runLen, OutStart: n.runOut, OutLen: n.runLen})
		n.runOrig, n.runLen = -1, 0
	}
	_ = o
}

func (n *Normalizer) copyByte(i int, b byte) error {
	if n.cfg.MaxOutput > 0 && len(n.out) >= n.cfg.MaxOutput {
		return &OffsetError{Err: ErrOutputLimit, Offset: i}
	}
	if n.runLen == 0 || n.runOrig+n.runLen != i || n.runOut+n.runLen != len(n.out) {
		n.flushRun(i)
		n.runOrig, n.runOut = i, len(n.out)
	}
	n.out = append(n.out, b)
	n.runLen++
	return nil
}

func (n *Normalizer) deleteTo(i, outAt int) {
	n.flushRun(i)
	n.mp.Add(span.Run{OrigStart: i, OrigLen: 0, OutStart: outAt, OutLen: 0})
}

// emitLine 输出一个换行，origPos 是其在原文的锚点（\n 或孤立 \r 的偏移）。
func (n *Normalizer) emitLine(i int) error {
	if err := n.copyByte(i, '\n'); err != nil {
		return err
	}
	n.track.LineEnd()
	return nil
}

func (n *Normalizer) step(i int, b byte) error {
	if n.cfg.StrictNUL && b == 0 {
		return &OffsetError{Err: ErrNUL, Offset: i}
	}
	wasPending := n.dec.PendingCR()
	ev, err := n.dec.Step(b)
	if err != nil {
		return err
	}
	switch ev {
	case eol.CRLine:
		// 待定 \r 确认成独立行尾；当前字节 b 尚未消费，需重放。
		if err := n.emitLine(i - 1); err != nil {
			return err
		}
		return n.step(i, b)
	case eol.LFLine:
		pos := i
		if wasPending {
			pos = i // \r\n：\r 与 \n 都映射到该换行
		}
		return n.emitLine(pos)
	case eol.PendingCR:
		// \r 之前若有待定空白，它在“行即将结束”前仍需保留观察：不输出。
		return nil
	default:
		if ws.IsSpace(b) {
			if n.track.Pending() == 0 && len(n.pendBuf) == 0 {
				// 首个待定空白：关闭拷贝区间
				n.flushRun(i)
			}
			if n.cfg.MaxPending > 0 && len(n.pendBuf) >= n.cfg.MaxPending {
				return &OffsetError{Err: ErrPendingLimit, Offset: i}
			}
			n.pendBuf = append(n.pendBuf, b)
			n.track.Space()
			return nil
		}
		// 非空白内容：释放待定空白为行中空白。
		for k, sb := range n.pendBuf {
			if err := n.copyByte(i-len(n.pendBuf)+k, sb); err != nil {
				return err
			}
		}
		n.pendBuf = n.pendBuf[:0]
		n.track.Content()
		return n.copyByte(i, b)
	}
}

// Write 喂入一个切分；任意切分方式结果一致。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.closed || n.terminal != nil {
		return 0, &OffsetError{Err: ErrClosed, Offset: n.consumed}
	}
	start := n.consumed
	for k, b := range p {
		if err := n.step(start+k, b); err != nil {
			var oe *OffsetError
			if errors.As(err, &oe) && oe.Err != ErrClosed {
				n.terminal = oe
			}
			n.consumed = start + k
			return k, err
		}
	}
	n.consumed = start + len(p)
	return len(p), nil
}

func (n *Normalizer) finish(final bool) {
	n.flushRun(n.consumed)
	if final {
		// EOF：悬着的 \r 是孤立行尾（\r 映射到该换行）；随后空白按行尾空白删除。
		if n.dec.PendingCR() {
			_ = n.emitLine(n.consumed - 1)
			n.flushRun(n.consumed)
		}
		if len(n.pendBuf) > 0 {
			n.mp.Add(span.Run{OrigStart: n.consumed - len(n.pendBuf), OrigLen: len(n.pendBuf), OutStart: len(n.out)})
			n.pendBuf = n.pendBuf[:0]
			n.track.Flush()
		}
		applyPolicy(&n.out, &n.mp, n.cfg.Policy)
	}
}

// Close 结束流并应用末尾策略。
func (n *Normalizer) Close() error {
	if n.closed {
		return &OffsetError{Err: ErrClosed, Offset: n.consumed}
	}
	n.closed = true
	n.finish(true)
	return nil
}

// Output 返回规范化输出（Close 后完整）。
func (n *Normalizer) Output() []byte { return n.out }

// Map 返回偏移映射。
func (n *Normalizer) Map() *span.Builder { return &n.mp }

// PendingBuf 返回尚未判定的尾部空白原文字节（供 par 拼接）。
func (n *Normalizer) PendingBuf() []byte { return n.pendBuf }

// LeadPending 报告输出首字节是否为以待定形式保留的空白（段首待定标记）。
func (n *Normalizer) LeadPending() bool {
	return len(n.out) > 0 && ws.IsSpace(n.out[0]) && len(n.pendBuf) > 0
}

// applyPolicy 原地应用末尾策略并同步截断映射。
func applyPolicy(out *[]byte, mp *span.Builder, p Policy) {
	t := 0
	for t < len(*out) && (*out)[len(*out)-1-t] == '\n' {
		t++
	}
	allNL := t == len(*out)
	switch p {
	case ExactlyOne:
		if len(*out) == 0 || allNL {
			if t > 0 {
				*out, t = (*out)[:1], 1
			}
		}
		if t > 1 {
			*out = (*out)[:len(*out)-t+1]
		}
	case TrimEmpty:
		if allNL {
			*out = (*out)[:0]
		} else if t > 1 {
			*out = (*out)[:len(*out)-t+1]
		} else {
			return
		}
	default:
		return
	}
	mp.CutOutput(len(*out))
}

// Normalize 一次性规范化整个缓冲区（供便捷使用与 par 复用）。
func Normalize(data []byte, cfg Config) ([]byte, *span.Builder, error) {
	n := New(cfg)
	if _, err := n.Write(data); err != nil {
		return n.out, &n.mp, err
	}
	_ = n.Close()
	return n.out, &n.mp, nil
}

// Segment 对一段做 Keep 核心规范化，但不应用 EOF 判定：
// 尾部悬着的 \r 与待定空白原样保留在 Segment 中，交给 par 修正。
func RunSegment(data []byte, base int) Segment {
	n := New(Config{Policy: Keep})
	_, _ = n.Write(data)
	n.flushRun(base + len(data))
	return Segment{
		Out:       n.out,
		Map:       n.mp,
		PendingCR: n.dec.PendingCR(),
		Pending:   append([]byte(nil), n.pendBuf...),
		PendOrig:  base + len(data) - len(n.pendBuf),
		LeadPend:  n.LeadPending(),
	}
}
