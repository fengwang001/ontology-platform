package stream

import "ontology/u8"

// Write 喂入输入字节。返回的 n 是本次 p 中已提交（与输出对应）的字节数；
// 残留在切分缓存（≤3 字节）里的字节不算已消费。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	if t.closed {
		t.term = ErrTerminal
		return 0, ErrTerminal
	}
	before := t.abs
	var err error
	if t.cfg.Dir == U16toU8 {
		err = t.write16(p)
	} else {
		err = t.write8(p)
	}
	n := int(t.abs - before)
	if n > len(p) {
		n = len(p)
	}
	if err != nil {
		t.term = err
	}
	return n, err
}

// write8 驱动 u8 DFA。游标 i 只在字节被消费时前进；Fed=false 时原地重放，
// 因此每个字节最多被检查两次（一次前缀判定、一次重放）。
func (t *Transcoder) write8(p []byte) error {
	i := 0
	for i < len(p) {
		t.st.Checks++
		ev, ok := t.dec8.Step(p[i])
		if !ok {
			i++
			continue
		}
		if !ev.Fed {
			// 当前字节未被吞：先提交前缀单元，再原地重放该字节。
			if err := t.deliver8(ev); err != nil {
				return err
			}
			continue
		}
		i++
		if err := t.deliver8(ev); err != nil {
			return err
		}
	}
	return nil
}

// deliver8 处理一个完整单元：流首 BOM、严格错误、替换、上限。
func (t *Transcoder) deliver8(ev u8.Event) error {
	if !ev.Bad && ev.Rune == 0xFEFF && !t.bomSeen && !t.cfg.noBOM &&
		t.dec8.Seen() == int64(ev.Consumed) {
		t.bomSeen = true
		if !t.emitBOM(ev.Consumed) {
			return &LimitError{}
		}
		return nil
	}
	if ev.Bad && t.cfg.Strict && t.abs+int64(ev.Consumed) > int64(t.cfg.headSkip) {
		off := t.abs
		if off < int64(t.cfg.headSkip) {
			off = int64(t.cfg.headSkip)
		}
		return &ByteError{Kind: ErrIllegal, Offset: off, Len: ev.Consumed}
	}
	if !t.emit(ev.Rune, ev.Bad, ev.Consumed) {
		return &LimitError{Head: append([]byte(nil), t.dec8.Pending()...)}
	}
	return nil
}

// Close 结束流：替换模式把残留前缀输出为一个 FFFD；严格模式报可区分的截断错误。
func (t *Transcoder) Close() error {
	if t.term != nil {
		err := t.term
		t.term = ErrTerminal
		return err
	}
	if t.closed {
		return ErrTerminal
	}
	t.closed = true
	if t.cfg.Dir == U16toU8 {
		return t.close16()
	}
	if ev, ok := t.dec8.Flush(); ok {
		if t.cfg.Strict {
			t.term = &ByteError{Kind: ErrTruncated, Offset: t.abs, Len: ev.Consumed}
			return t.term
		}
		if !t.emit(ev.Rune, true, ev.Consumed) {
			t.term = &LimitError{Head: append([]byte(nil), t.dec8.Pending()...)}
			return t.term
		}
	}
	return nil
}
