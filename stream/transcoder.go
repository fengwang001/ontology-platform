package stream

import (
	"ontology/u16"
	"ontology/u8"
)

// Transcoder 是流式转码器，实现 Write/Close/Output。单实例非并发安全。
type Transcoder struct {
	cfg      Config
	d8       *u8.Decoder
	d16      *u16.Decoder
	out      []byte
	stats    Stats
	terminal bool
	termErr  error
	closed   bool

	// Fragment：Close 时不报截断、不为挂起前缀产 FFFD（par 用）。
	Fragment bool
}

// New 按 cfg 构造转码器。
func New(cfg Config) *Transcoder {
	t := &Transcoder{cfg: cfg}
	if cfg.From == UTF8 {
		t.d8 = &u8.Decoder{}
	} else {
		o := u16.LittleEndian
		if cfg.From == UTF16BE {
			o = u16.BigEndian
		}
		t.d16 = u16.NewDecoder(o)
	}
	if cfg.EmitBOM {
		t.writeRune(0xFEFF)
	}
	return t
}

// Output 返回已产出的输出字节。
func (t *Transcoder) Output() []byte { return t.out }

// Stats 返回统计快照，满足
// 合法字节 + 非法吞字节 + BOM 字节 == 已消费输入字节。
func (t *Transcoder) Stats() Stats {
	s := t.stats
	if t.d8 != nil {
		s.Checked = t.d8.Checked
	} else {
		s.Checked = t.d16.Checked
	}
	return s
}

func (t *Transcoder) pendingLen() int {
	if t.d8 != nil {
		return t.d8.PendingLen()
	}
	return t.d16.PendingLen()
}

type inUnit struct {
	r     rune
	ok    bool
	size  int
	from  int64
	trunc bool
	bom   bool
}

// Write 喂入新字节。返回的 n 是本次被闭合消费的字节数；
// 末尾挂起的合法前缀不属于 n，调用方下次应从 n 处继续（含挂起字节）。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	if t.terminal {
		return 0, t.termErr
	}
	base := t.stats.Consumed + t.cfg.OffsetBase
	var raw []inUnit
	if t.d8 != nil {
		for _, u := range t.d8.Feed(p, base) {
			raw = append(raw, inUnit{u.R, u.Valid, u.Size, u.Start, false, false})
		}
	} else {
		for _, u := range t.d16.Feed(p, base) {
			raw = append(raw, inUnit{u.R, u.Valid, u.Size, u.Start, u.Trunc, u.IsBOM})
		}
	}
	var closed int
	for _, u := range raw {
		if err := t.handle(u); err != nil {
			t.die(err)
			return closed, err
		}
		closed += u.size
	}
	t.stats.Consumed += int64(len(p)) - int64(t.pendingLen())
	return closed, nil
}

// Close 结束流。替换模式下挂起前缀产 1 个 U+FFFD；严格模式报 ErrTruncated。
// Fragment 模式下挂起前缀原样挂起、不报错（交给下一段）。
func (t *Transcoder) Close() error {
	if t.terminal {
		return t.termErr
	}
	if t.closed {
		return ErrClosed
	}
	if t.Fragment {
		t.closed = true
		return nil
	}
	var u inUnit
	var have bool
	if t.d8 != nil {
		x, ok := t.d8.Flush()
		u, have = inUnit{x.R, false, x.Size, x.Start, true, false}, ok
	} else {
		x, ok := t.d16.Flush()
		u, have = inUnit{x.R, false, x.Size, x.Start, true, false}, ok
	}
	if have {
		if err := t.handle(u); err != nil {
			t.die(err)
			return err
		}
		t.stats.Consumed += int64(u.size)
	}
	t.closed = true
	t.die(nil)
	return nil
}

func (t *Transcoder) handle(u inUnit) error {
	if u.bom && !t.cfg.MidStream { // 流开头 BOM：按配置丢弃或输出
		t.stats.BOMBytes += int64(u.size)
		if t.cfg.EmitBOM {
			return t.writeRune(0xFEFF)
		}
		return nil
	}
	if u.bom {
		// 流中间 U+FEFF：普通标量，原样保留，落入标量分支。
	}
	if !u.ok {
		t.stats.Illegal++
		t.stats.IllegalBytes += int64(u.size)
		if t.cfg.Strict {
			err := ErrIllegal
			if u.trunc {
				err = ErrTruncated
			}
			return &UnitError{Err: err, Offset: u.from, Size: u.size}
		}
		return t.writeRune(0xFFFD)
	}
	t.stats.Scalars++
	return t.writeRune(u.r)
}

func (t *Transcoder) writeRune(r rune) error {
	n := t.encLen(r)
	if t.cfg.MaxOut > 0 && len(t.out)+n > t.cfg.MaxOut {
		return ErrLimit
	}
	if t.cfg.To == UTF8 {
		t.out = u8.Encode(t.out, r)
	} else {
		t.out = u16.Encode(t.out, r, t.outOrder())
	}
	return nil
}

func (t *Transcoder) outOrder() u16.Order {
	if t.cfg.To == UTF16BE {
		return u16.BigEndian
	}
	return u16.LittleEndian
}

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.To != UTF8 {
		return u16.EncLen(r)
	}
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

func (t *Transcoder) die(err error) { t.terminal, t.termErr = true, err }
