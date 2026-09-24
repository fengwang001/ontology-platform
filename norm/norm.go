// Package norm 是流式行尾与空白规范化器。单实例非并发安全，多实例互不干扰。
package norm

import (
	"ontology/eol"
	"ontology/span"
)

// Trailing 是末尾换行策略。
type Trailing uint8

const (
	Keep      Trailing = iota // 保留原样
	EnsureOne                 // 非空无尾换行则补一个；空输入仍为空
	Trim                      // 删除全部末尾空行，非空保留恰好一个换行
)

// Config 配置上限与严格模式；Limit<=0 表示不限制。
type Config struct {
	Mode            Trailing
	StrictNUL       bool
	WhitespaceLimit int
	OutputLimit     int
}

// Kind 是可判定的错误类别。
type Kind uint8

const (
	ErrNUL Kind = iota + 1
	ErrWhitespaceLimit
	ErrOutputLimit
	ErrClosed
)

// Error 是带类别与原文偏移的终态错误。
type Error struct {
	Kind   Kind
	Offset int
}

func (e Error) Error() string {
	s := [...]string{"", "norm: NUL byte", "norm: whitespace buffer limit exceeded",
		"norm: output limit exceeded", "norm: normalizer closed"}
	return s[e.Kind]
}

// Is 让 errors.Is 按类别匹配。
func (e Error) Is(target error) bool {
	t, ok := target.(Error)
	return ok && t.Kind == e.Kind
}

type Normalizer struct {
	cfg    Config
	dec    eol.Decoder
	out    []byte
	segs   []span.Seg
	ap, op int    // 已决原文末位 / 已输出末位
	pend   []byte // 未决原文字节（全局起点为 ap）
	wsRel  int    // 待定空白在 pend 中的起点，-1 表示无
	fed    int    // 已喂入原文字节总数
	done   bool
	term   Kind
}
func New(cfg Config) *Normalizer { return &Normalizer{cfg: cfg, wsRel: -1} }
func (n *Normalizer) fail(k Kind, off int) error {
	n.done, n.term = true, k
	return Error{Kind: k, Offset: off}
}

// copyBytes 把 data（pend 某前缀）记为存活；toNL 时逐字节替换成 \n。
func (n *Normalizer) copyBytes(data []byte, toNL bool) {
	if len(data) == 0 {
		return
	}
	if m := len(n.segs); m > 0 {
		if p := &n.segs[m-1]; !toNL && p.A0+p.OrigLen == n.ap &&
			p.O0+p.OutLen == n.op && p.OrigLen > 0 && p.OutLen > 0 {
			p.OrigLen += len(data)
			p.OutLen += len(data)
			goto emit
		}
	}
	n.segs = append(n.segs, span.Seg{A0: n.ap, OrigLen: len(data), O0: n.op, OutLen: len(data)})
emit:
	if toNL {
		for range data {
			n.out = append(n.out, '\n')
		}
	} else {
		n.out = append(n.out, data...)
	}
	n.ap += len(data)
	n.op += len(data)
}

func (n *Normalizer) deleteBytes(length int) {
	if length == 0 {
		return
	}
	n.segs = append(n.segs, span.Seg{A0: n.ap, OrigLen: length, O0: n.op})
	n.ap += length
}

// lfEnd 处理以 \n 结尾的行尾；crlf 表示前一字节是配对 \r（\r 变换、\n 删除）。
func (n *Normalizer) lfEnd(nlPos int, crlf bool) error {
	cnt, nlRel := len(n.pend), len(n.pend)-1
	rel := nlRel
	if crlf {
		rel = nlRel - 1 // \r 为换行锚点；\n 将被删除
	}
	ws := rel
	if n.wsRel >= 0 && n.wsRel < rel {
		ws = n.wsRel
	}
	n.copyBytes(n.pend[:ws], false)
	n.deleteBytes(rel - ws) // 行尾空白（CRLF 时空区间，\r 自身做变换）
	if n.cfg.OutputLimit > 0 && n.op+1 > n.cfg.OutputLimit {
		n.pend = n.pend[cnt:]
		return n.fail(ErrOutputLimit, nlPos)
	}
	n.copyBytes(n.pend[rel:rel+1], true) // \r(CRLF) 或 \n(独立) 变成 \n
	if crlf {
		n.deleteBytes(1) // CRLF 的 \n 删除
	}
	n.pend = n.pend[cnt:]
	n.wsRel = -1
	return nil
}

// crAlone 把独立行尾 \r（pend[:cnt] 末位）变换为 \n。
func (n *Normalizer) crAlone(crPos, cnt int) error {
	crRel := cnt - 1
	ws := crRel
	if n.wsRel >= 0 && n.wsRel < crRel {
		ws = n.wsRel
	}
	n.copyBytes(n.pend[:ws], false)
	n.deleteBytes(crRel - ws)
	if n.cfg.OutputLimit > 0 && n.op+1 > n.cfg.OutputLimit {
		n.pend = n.pend[:crRel]
		return n.fail(ErrOutputLimit, crPos)
	}
	n.copyBytes(n.pend[crRel:crRel+1], true)
	n.pend = n.pend[cnt:]
	n.wsRel = -1
	return nil
}

// literals 决掉 pend[:cnt] 为存活普通字节。
func (n *Normalizer) literals(cnt int) {
	n.copyBytes(n.pend[:cnt], false)
	n.pend = n.pend[cnt:]
	n.wsRel = -1
}
