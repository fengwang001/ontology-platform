package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u8"
	"ontology/u16"
)

type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

type Config struct {
	Src       Encoding
	Dst       Encoding
	Strict    bool
	MaxOutput int  // <=0 不限
	EmitBOM   bool // 起始 BOM 是否输出
	NoBOM     bool // par 中间段：不识别起始 BOM
}

type Stats struct {
	Valid, ValidBytes int
	Bad, BadBytes     int
	BOMBytes          int
	Truncated         int
}

var (
	ErrOutputLimit = errors.New("stream: output exceeds limit")
	ErrTerminal    = errors.New("stream: write after terminal state")
)

type InvalidError struct{ Offset, Len int }
func (e *InvalidError) Error() string { return "stream: invalid byte unit" }

type TruncatedError struct{ Offset, Len int }
func (e *TruncatedError) Error() string { return "stream: truncated input" }

type Transcoder struct {
	cfg     Config
	endian  u16.Endian
	out     []byte
	pend    []byte
	stats   Stats
	checks  int64
	closed  bool
	dead    bool
	err     error
	consum  int  // 已定性字节数（含已进入 pend 后又定性）
	st      int  // 0 空闲；正数为待续状态（见 feed）
	lead    byte
	r       rune
	first   bool
	badOff  int
	badLen  int
	utf16   bool
	pendOff int // pend 首字节在全流中的偏移
}

func New(cfg Config) *Transcoder {
	t := &Transcoder{pend: make([]byte, 0, 4), first: true}
	t.cfg = cfg
	t.utf16 = cfg.Src == UTF16LE || cfg.Src == UTF16BE
	if cfg.Src == UTF16BE {
		t.endian = u16.BE
	}
	return t
}

// 状态机返回值。
const (
	sOK = iota
	sBad
	sLimit
)

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.dead {
		return 0, t.err
	}
	start := t.consum
	for i := 0; i < len(p); i++ {
		t.checks++
		if r := t.feed(p[i]); r == sBad {
			t.die(&InvalidError{Offset: t.badOff, Len: t.badLen})
			return t.consum - start, t.err
		} else if r == sLimit {
			t.die(ErrOutputLimit)
			return t.consum - start, t.err
		}
	}
	return t.consum - start, nil
}

func (t *Transcoder) die(e error) { t.dead = true; t.err = e }

func (t *Transcoder) Close() error {
	if t.dead {
		return t.err
	}
	if t.closed {
		return nil
	}
	t.closed = true
	if len(t.pend) == 0 {
		return nil
	}
	off := t.pendOff
	n := len(t.pend)
	if t.cfg.Strict {
		t.die(&TruncatedError{Offset: off, Len: n})
		return t.err
	}
	if !t.emit(u8.RuneError) {
		t.die(ErrOutputLimit)
		return t.err
	}
	t.stats.Truncated += n
	return nil
}

func (t *Transcoder) Output() []byte { return t.out }
func (t *Transcoder) Stats() Stats   { return t.stats }
func (t *Transcoder) Checks() int64  { return t.checks }
func (t *Transcoder) Err() error     { return t.err }
func (t *Transcoder) Pending() []byte {
	return append([]byte(nil), t.pend...)
}

// feed 喂入一个源字节；状态机定义见 DESIGN.md 第 1、2 节。
func (t *Transcoder) feed(b byte) int {
	if t.utf16 {
		return t.feed16(b)
	}
	return t.feed8(b)
}

// 以下两个文件段（feed8/feed16/emit/边界辅助）分文件放置，见 feed.go。
