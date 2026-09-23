// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
// 单个 Transcoder 实例不是并发安全的；并发请用 par 包。
package stream

import (
	"errors"

	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// 四类可判定错误：用 errors.Is 即可区分。
var (
	// ErrIllegalByte：遇到非法单元（严格模式）。
	ErrIllegalByte = errors.New("stream: illegal byte unit")
	// ErrTruncated：流结束时残留未完成的合法前缀。
	ErrTruncated = errors.New("stream: truncated input")
	// ErrLimit：输出会越过配置上限，已停在标量边界。
	ErrLimit = errors.New("stream: output limit exceeded")
	// ErrClosed：终态（出错或关闭）之后继续 Write。
	ErrClosed = errors.New("stream: writer is in terminal state")
)

// OffsetError 携带问题单元在整个输入流中的起始偏移与长度。
type OffsetError struct {
	Op     error // ErrIllegalByte 或 ErrTruncated
	Offset int64
	Length int
}

func (e *OffsetError) Error() string { return e.Op.Error() }
func (e *OffsetError) Unwrap() error { return e.Op }

// Encoding 标识一端的编码。
type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	UTF16BOM // 仅用于输入：由流首 BOM 定序，缺省按 LE
)

// Config 配置转码器。
type Config struct {
	From, To Encoding
	Strict   bool   // true=严格模式；false=替换模式
	KeepBOM  bool   // true=流首 BOM 作为 U+FEFF 保留；false=丢弃
	EmitBOM  bool   // 输出为 UTF-16 时在开头写出 BOM
	MaxOut   int    // 输出字节上限；0 表示不限
	BaseOff  int64  // par 使用：错误偏移的基址
	SkipBOM  bool   // par 使用：本段不是首段，禁止识别流首 BOM
}

// Stats 是字节守恒统计。
type Stats struct {
	Scalars      int64 // 已接受的合法标量数
	IllegalUnits int64 // 非法单元数
	IllegalBytes int64 // 被非法单元吞掉的字节数
	BOMBytes     int64 // 被识别为流首 BOM 的输入字节数
	Consumed     int64 // 已消费输入总字节数
	Checks       int64 // 字节被检查的总次数
}

// Transcoder 是流式转码器。
type Transcoder struct {
	cfg  Config
	out  []byte
	d8   *u8.Decoder
	d16  *u16.Decoder
	st   Stats
	term error // 终态错误
	// lastOff/lastLen 最近一个事件覆盖的输入区间，供偏移记账。
	consumedInWrite int
	limitHit        bool
}

// New 创建转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	if cfg.From == UTF8 {
		t.d8 = &u8.Decoder{}
	} else {
		var o *u16.Order
		if cfg.From == UTF16LE {
			v := u16.LE
			o = &v
		} else if cfg.From == UTF16BE {
			v := u16.BE
			o = &v
		}
		t.d16 = u16.NewDecoder(o)
	}
	if cfg.EmitBOM && cfg.To != UTF8 && !cfg.SkipBOM {
		order := u16.LE
		if cfg.To == UTF16BE {
			order = u16.BE
		}
		t.out = append(t.out, u16.BOMBytes(order)...)
	}
	return t
}

type evKind uint8

const (
	evScalar evKind = iota
	evIllegal
)

type event struct {
	kind evKind
	r    rune
	size int
}

// Write 喂入一段输入，返回本次被消费的字节数。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	if t.limitHit {
		return 0, t.wrap(ErrLimit)
	}
	startConsumed := t.st.Consumed
	events := make([]event, 0, 8)
	fn := func(r rune, illegal bool, size int) {
		k := evScalar
		if illegal {
			k = evIllegal
		}
		events = append(events, event{k, r, size})
	}
	if t.cfg.From == UTF8 {
		t.d8.Feed(p, func(e u8.Event) {
			fn(e.R, e.Kind == u8.Illegal, e.Size)
		})
	} else {
		t.d16.Feed(p, func(e u16.Event) {
			fn(e.R, e.Kind == u16.Illegal, e.Size)
		})
	}
	consumedNow := int(t.st.Consumed - startConsumed)
	_ = consumedNow
	for _, e := range events {
		if e.kind == evIllegal {
			t.st.IllegalUnits++
			t.st.IllegalBytes += int64(e.size)
			if t.cfg.Strict {
				t.term = &OffsetError{Op: ErrIllegalByte,
					Offset: t.cfg.BaseOff + t.st.Consumed, Length: e.size}
				t.st.Consumed += int64(e.size)
				t.syncChecks()
				return int(t.st.Consumed - startConsumed), t.term
			}
			e.r = scalar.Replacement
		}
		if !t.commit(e.r, e.size) {
			t.st.Consumed += int64(e.size)
			t.syncChecks()
			n := int(t.st.Consumed - startConsumed)
			return n, t.wrap(ErrLimit)
		}
		t.st.Consumed += int64(e.size)
		if e.kind == evScalar {
			t.st.Scalars++
		}
	}
	t.syncChecks()
	return int(t.st.Consumed - startConsumed), nil
}

// commit 把一个完整标量追加到输出；超限时返回 false 且不写半个标量。
func (t *Transcoder) commit(r rune, _ int) bool {
	if r == scalar.BOM && t.st.Scalars == 0 && !t.cfg.SkipBOM {
		// 流首 BOM：记账为 BOM 字节，是否输出取决于 KeepBOM。
		if t.cfg.From == UTF8 {
			t.st.BOMBytes += 3
		} else {
			t.st.BOMBytes += 2
		}
		if !t.cfg.KeepBOM {
			return true
		}
	}
	add := t.encoded(r)
	if t.cfg.MaxOut > 0 && len(t.out)+len(add) > t.cfg.MaxOut {
		t.limitHit = true
		return false
	}
	t.out = append(t.out, add...)
	return true
}

func (t *Transcoder) encoded(r rune) []byte {
	if t.cfg.To == UTF8 {
		return u8.Encode(nil, r)
	}
	o := u16.LE
	if t.cfg.To == UTF16BE {
		o = u16.BE
	}
	if ord, ok := t.d16.Order(); t.cfg.From != UTF8 && !ok {
		_ = ord
	}
	return u16.Encode(nil, r, o)
}

func (t *Transcoder) syncChecks() {
	if t.cfg.From == UTF8 {
		t.st.Checks = t.d8.Checks()
	} else {
		t.st.Checks = t.d16.Checks()
	}
}

// Close 结束流：处理残留的未完成前缀。
func (t *Transcoder) Close() error {
	if t.term != nil {
		return t.term
	}
	if t.limitHit {
		t.term = t.wrap(ErrLimit)
		return t.term
	}
	var n int
	if t.cfg.From == UTF8 {
		n = t.d8.Truncated()
	} else {
		n = t.d16.Truncated()
	}
	if n > 0 {
		t.st.Consumed += int64(n)
		if t.cfg.Strict {
			t.term = &OffsetError{Op: ErrTruncated,
				Offset: t.cfg.BaseOff + t.st.Consumed - int64(n), Length: n}
			t.syncChecks()
			return t.term
		}
		if !t.commit(scalar.Replacement, n) {
			t.syncChecks()
			t.term = t.wrap(ErrLimit)
			return t.term
		}
	}
	t.syncChecks()
	t.term = ErrClosed
	return nil
}

// Output 返回已产出的字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.st }

// Pending 返回尚未判定的缓存字节数（UTF-8 上限 3，UTF-16 上限 2）。
func (t *Transcoder) Pending() int {
	if t.cfg.From == UTF8 {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}

func (t *Transcoder) wrap(err error) error { return err }
