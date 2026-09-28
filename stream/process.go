package stream

import "ontology/scalar"

// Write feeds input bytes. n counts bytes of fully resolved units; bytes still
// held in an incomplete prefix are not consumed.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.termErr != nil || t.closed {
		return 0, t.terminal(ErrClosed)
	}
	if !t.started {
		t.started = true
		if t.cfg.EmitBOM {
			if err := t.emit(0xFEFF); err != nil {
				return 0, err
			}
		}
	}
	if !t.cfg.NoBOMScan && t.stats.Consumed == 0 && t.PendingLen() == 0 {
		p = t.stripBOM(p)
	}
	n := 0
	for len(p) > 0 {
		r, size, bad, reprocess, err := t.nextUnit(p)
		if err != nil {
			t.termErr = err
			return n, err
		}
		if size == 0 {
			break // incomplete prefix; remaining bytes are buffered
		}
		if err := t.commit(r, size, bad); err != nil {
			t.termErr = err
			return n, err
		}
		n += size
		p = p[size:]
		if reprocess {
			p = p[size-1:] // invalidating byte is re-read as a new unit
			n--
		}
		_ = n
	}
	return n, nil
}

// Close finalizes the stream; a buffered valid prefix is one truncated unit.
func (t *Transcoder) Close() error {
	if t.termErr != nil {
		return t.termErr
	}
	if t.closed {
		return t.terminal(ErrClosed)
	}
	t.closed = true
	if len(t.bomBuf) > 0 {
		buf := append([]byte(nil), t.bomBuf...)
		t.bomBuf = t.bomBuf[:0]
		if _, err := t.Write(buf); err != nil {
			return err
		}
	}
	size := t.PendingLen()
	if size == 0 {
		return nil
	}
	if t.cfg.Dir == U8ToU8 {
		t.dec8.Drain()
	} else {
		t.dec16.Drain()
	}
	if t.cfg.Strict {
		err := &OffsetError{Err: ErrTruncated, Offset: t.stats.Consumed, Size: size}
		t.termErr = err
		return err
	}
	t.stats.BadUnits++
	t.stats.BadBytes += size
	t.stats.Consumed += size
	t.stats.Truncated = true
	return t.emit(scalar.Replacement)
}

func (t *Transcoder) commit(r rune, size int, bad bool) error {
	if bad {
		t.stats.BadUnits++
		t.stats.BadBytes += size
		if t.cfg.Strict {
			return &OffsetError{Err: ErrInvalidByte, Offset: t.stats.Consumed, Size: size}
		}
		r = scalar.Replacement
	} else {
		t.stats.Scalars++
	}
	if err := t.emit(r); err != nil {
		return err
	}
	t.stats.Consumed += size
	return nil
}

func (t *Transcoder) terminal(err error) error {
	if t.termErr == nil {
		t.termErr = err
	}
	return t.termErr
}

func (t *Transcoder) emit(r rune) error {
	n := encLen(t.cfg.Dir, r)
	if t.cfg.MaxOutput > 0 && len(t.out)+n > t.cfg.MaxOutput {
		return &OffsetError{Err: ErrOutputLimit, Offset: t.stats.Consumed, Size: 0}
	}
	t.out = encode(t.out, t.cfg.Dir, r)
	return nil
}
