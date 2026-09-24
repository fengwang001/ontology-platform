// Package stream 提供带非法字节替换的流式 UTF-8⇄UTF-16 转码器。
// 单个 Transcoder 非并发安全；并发转码见 par 包。
package stream

import (
	"errors"

	"ontology/scalar"
)

var (
	ErrIllegal     = errors.New("stream: illegal byte")
	ErrTruncated   = errors.New("stream: truncated input")
	ErrOutputLimit = errors.New("stream: output exceeds limit")
	ErrTerminal    = errors.New("stream: transcoder already in terminal error state")
)

// Error 携带可判定错误与该单元在整个输入流中的起始偏移与长度。
type Error struct {
	Err    error
	Offset int64
	Len    int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Dir int

const (
	U8toU16LE Dir = iota
	U8toU16BE
	U16LEtoU8
	U16BEtoU8
)

// Config 配置转码器。Limit<=0 不限制；NoLeadingBOM 供 par 非首段使用。
type Config struct {
	Dir          Dir
	Strict       bool
	EmitBOM      bool
	Limit        int
	NoLeadingBOM bool
}

// Stats 是字节守恒统计；Checks 为字节被检查总次数。
type Stats struct {
	Scalars  int64
	BadUnits int64
	BadBytes int64
	BOMBytes int64
	Consumed int64
	Checks   int64
	MaxHold  int
}

type bomState int

const (
	bomUnknown bomState = iota
	bomNone
	bomHandled
)

// Transcoder 是流式转码器，非并发安全。
type Transcoder struct {
	cfg     Config
	out     []byte
	term    error
	st      Stats
	abs     int64
	hold    []byte // 未结案原始字节：u8<=3；u16<=1（另可能缓存待重放 code unit）
	pendHi  int32
	pendOff int64
	bom     bomState
}

func New(cfg Config) *Transcoder {
	return &Transcoder{cfg: cfg, hold: make([]byte, 0, 3), pendHi: -1}
}

func (t *Transcoder) Stats() Stats   { return t.st }
func (t *Transcoder) Output() []byte { return t.out }

// Write 喂入输入；n 为本次已结案字节数（hold 中的字节不算已消费）。
func (t *Transcoder) Write(p []byte) (n int, err error) {
	if t.term != nil {
		return 0, t.term
	}
	start := t.st.Consumed
	for n < len(p) {
		stop, adv := t.feed(p[n:])
		n += adv
		if stop {
			break
		}
	}
	if t.cfg.Dir <= U8toU16BE && len(t.hold) > t.st.MaxHold {
		t.st.MaxHold = len(t.hold)
	}
	return int(t.st.Consumed - start), t.term
}

// Close 结束流：替换模式残留前缀输出一个 FFFD；严格模式返回截断错误。
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	if len(t.hold) == 0 && t.pendHi < 0 {
		return nil
	}
	off, n := t.abs, len(t.hold)
	if t.pendHi >= 0 {
		off, n = t.pendOff, 2
	}
	// UTF-8 单首字节只有在真正流末才作为截断/替换；其计 1 字节。
	if t.cfg.Strict {
		return t.fail(&Error{Err: ErrTruncated, Offset: off, Len: n})
	}
	if e := t.emit(scalar.Replacement); e != nil {
		return t.fail(e)
	}
	// u16 待配高代理补记 2；UTF-8 吞掉整个 hold（单首字节也在此处=1）。
	consumed := len(t.hold)
	if t.pendHi >= 0 {
		consumed = 2
	}
	t.st.BadUnits++
	t.st.BadBytes += int64(consumed)
	t.st.Consumed += int64(consumed)
	t.abs += int64(consumed)
	t.hold = t.hold[:0]
	t.pendHi = -1
	return nil
}

func (t *Transcoder) fail(e error) error {
	if t.term == nil {
		t.term = e
	}
	return t.term
}

// emit 追加一个标量并检查上限；超限不改变任何状态。
func (t *Transcoder) emit(r rune) error {
	if t.cfg.Limit > 0 && len(t.out)+t.encLen(r) > t.cfg.Limit {
		return &Error{Err: ErrOutputLimit}
	}
	t.out = t.appendEnc(t.out, r)
	return nil
}
