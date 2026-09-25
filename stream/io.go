package stream

import "ontology/scalar"

// emit 处理一个解码单元；返回的 error 一旦非空即终态。
func (t *Transformer) emit(r rune, ok, incomp, isBOM bool, n int) error {
	switch {
	case incomp:
		if t.cfg.Strict {
			return ErrTruncated
		}
		t.st.Invalid++
		t.st.BadBytes += n
		t.st.Consumed += n
		r = scalar.Replacement
	case isBOM:
		t.st.BOMBytes += n
		t.st.Consumed += n
		if !t.cfg.KeepBOM {
			return nil
		}
		t.st.LegalBytes += n
		r, ok = scalar.BOM, true
	case !ok:
		if t.cfg.Strict {
			return &InvalidError{Offset: t.cfg.BaseOff + t.st.Consumed, Length: n}
		}
		t.st.Invalid++
		t.st.BadBytes += n
		t.st.Consumed += n
		r = scalar.Replacement
	default:
		t.st.Scalars++
		t.st.Consumed += n
		t.st.LegalBytes += n
	}
	if t.cfg.MaxOut > 0 && len(t.out)+t.rlen(r) > t.cfg.MaxOut {
		return ErrLimit
	}
	t.put(r)
	return nil
}

// Write 喂入输入；返回本次已判定消费的字节数与终态错误。
func (t *Transformer) Write(p []byte) (int, error) {
	if t.closed || t.err != nil {
		return t.st.Consumed, t.terminal()
	}
	before := t.st.Consumed
	for i := 0; i < len(p); i++ {
		var err error
		if t.d8 != nil {
			err = t.feedU8(p[i])
		} else {
			for _, u := range t.d16.Byte(p[i]) {
				if err = t.emit(u.R, u.OK, false, u.IsBOM, u.Consumed); err != nil {
					break
				}
			}
		}
		if err != nil {
			t.err = err
			return t.st.Consumed - before, err
		}
	}
	return t.st.Consumed - before, nil
}

func (t *Transformer) terminal() error {
	if t.closed && t.err == nil {
		return ErrTerminal
	}
	return t.err
}

func (t *Transformer) feedU8(b byte) error {
	if !t.cfg.NoInputBOM && t.headSt < 3 {
		if b == u8BOM[t.headSt] {
			t.headBuf = append(t.headBuf, b)
			t.headSt++
			if t.headSt < 3 {
				return nil
			}
			t.headBuf = nil
			return t.emit(scalar.BOM, true, false, true, 3)
		}
		old := t.headBuf
		t.headSt, t.headBuf, t.cfg.NoInputBOM = 3, nil, true
		for _, ob := range old {
			if err := t.units8(ob); err != nil {
				return err
			}
		}
	}
	return t.units8(b)
}

func (t *Transformer) units8(b byte) error {
	for _, u := range t.d8.Step(b) {
		if err := t.emit(u.R, u.OK, false, false, u.Consumed); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transformer) flushHead() error {
	old := t.headBuf
	t.headSt = 3
	for _, ob := range old {
		if err := t.units8(ob); err != nil {
			return err
		}
	}
	t.headBuf = nil
	return nil
}

// Close 结束流；残留前缀在替换模式产出一个 FFFD，严格模式报 ErrTruncated。
func (t *Transformer) Close() error {
	if t.err != nil || t.closed {
		if t.err == nil {
			t.err = ErrTerminal
		}
		return t.err
	}
	t.closed = true
	var err error
	skipEOF := t.cfg.NoEOF
	if t.d8 != nil {
		if !t.cfg.NoInputBOM && t.headSt < 3 {
			if !skipEOF {
				err = t.flushHead()
			} else {
				t.headSt = 3
				t.headBuf = nil
			}
		}
		if err == nil && !skipEOF {
			if u, ok := t.d8.Flush(); ok {
				err = t.emit(u.R, false, true, false, u.Consumed)
			}
		}
	} else if !skipEOF {
		if u, ok := t.d16.Flush(); ok {
			err = t.emit(u.R, false, true, false, u.Consumed)
		}
	}
	if err != nil {
		t.err = err
	}
	return err
}
