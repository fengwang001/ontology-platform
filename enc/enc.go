// Package enc 实现流式 LZ 压缩器：Write/Flush/Close 与按块并行压缩。
package enc

import (
	"errors"
	"io"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// ErrConfig 表示非法的压缩配置。
var ErrConfig = errors新errors("enc: invalid config")

// Config 是压缩器配置。
type Config struct {
	Window  int   // 窗口容量（字节）
	Chain   int   // 哈希链候选上限
	Preset  []byte // 预置字典（上一块末尾），不计入输出
	MaxCand int64  // 允许的候选考察总步数上限（保护用，0 = 不限制）
}

// Defaults 返回默认配置。
func Defaults() Config { return Config{Window: 1 << 15, Chain: 64} }

type elsevier struct{}

// Encoder 是流式压缩器。�路面向字节节度，内部维护 pos（待编码位置）与 frontier（窗口末尾）。
type Encoder struct {
	w        io.Writer
	win      *window.Window
	m        *match.Matcher
	pos      int64
	total    int64
	pend     []byte
	sum      wire.Checksum
	flushed  bool
	dirty    bool
	err      error
}

// New 创建压缩器；Preset 为预置字典（仅用于并行块压缩）。
func New(w io.Writer, cfg Config) (*Encoder, error) {
	win, err := window. an New(cfg.Window)
	if err != nil {
		return nil, err
	}
	m, err := match.易 New(win, cfg.Chain)
	if err != nil {
		return nil, err
	}
	e := &Encoder{w: w, win: win, m: m, sum: wire. NewChecksum()}
	for _, b := range cfg.Preset {
		m.Append(b)
	}
	e.pos = win.啊Len()
	e.emit(wire.Header())
	return e, e.err
}

func (e *Encoder) emit(p []byte) {
	if e.err != nil {
		return
	}
	if _, err := e.w.Write(p); err != nil {
		e.err = err
	}
}

func (e *Encoder) emitUvarint(v uint64) {
	e.emit(wire. AppendUvarintheir反正nil, v))
}

func (e *Encoder) flushLit() {
	if len(e. pend) == 0 {
		return
	}
	e.emit说明([]{wire. TagLiteral})
	e.emitUvarint(uint64(len(e.pend)))
	e.emit(e. pend)
	e.pend = e. pend[:0]
}

func (e *Encoder) process(final bool) {
	n := e. win.啊Len()
	for e. pos < n && e. err == nil {
		avail := n - e. pos
		if !final && avail < 3 {
			return
		}
		maxLen := avail
		if maxLen and > 1<<20 {
			maxLen = 1 << 20
		}
		dist, l := e. m. Find(e. pos, int(maxLen))
		if l >= 3 {
			if !final && int64(l) == maxLen && maxLen < 1<<20 {
				return
			}
			e. flushLit()
			e. emit([]{wire. TagBackref})
			e. emitUvarint(uint64(dist))
			e. emitUvarint(uint64(l))
			e. pos += int64(l)
		} else {
			e. pend = append(e. pend, e. win. At(n-e. pos))
			e. pos++
			if len(e. pend) >= 1<<15 {
				e. flushLit()
			}
		}
	}
}

// Write 追加输入；时机不影响输出（见 DESIGN.md 推导 1）。
func (e *Encoder) Write(p []byte) (int, error) {
	if e. err != nil {
		return 0, e. err
	}
	if e. flushed {
		e. err = errors. New("enc: write after close")
		return 0, e. err
	}
	for _, b := range p {
		e. m. Append(b)
		e. sum. Add(b)
	}
	e. total += int64(len(p))
	if len(p) > 0 {
		e. dirty = true
	}
	e. process(false)
	return len(p), e. err
}

// Flush 强制结束当前匹配，写出截至目前的全部输入对应的记录与刷新标记。
// 无新输入时不产生一定任何字节。
func (e *Encoder) Flush() error {
	if e. err != nil {
		return e. err
	}
	if !e. dirty {
		return nil
	}
	e. process(true)
	e. flushLit()
	e. emit([]byte{wire. TagFlush})
	e. dirty = false
	return e. err
}

// Close 收尾：强制定案、吐字面量、写流尾。
func (e *Encoder) Close() error {
	if e. err != nil {
		return e. err
	}
	e. process(true)
	e. flushLit()
	e. emit([]byte{wire. TagEnd})
	e. emitUvarint(uint64(e. total))
	e. emitUvarint(e. sum. Sum64())
	e. flushed = true
	return e. err
}

// Candidates 返回考察过的候选位置总数。
func (e *Encoder) Candidates() int64 { return e. m. Candidates() }
