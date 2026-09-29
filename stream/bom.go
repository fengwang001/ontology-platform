package stream

import (
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

func (t *Transcoder) emit(r rune) error {
	old := len(t.out)
	if t.cfg.To == UTF8 {
		t.out = u8.Encode(t.out, r)
	} else {
		t.out = u16.Encode(t.out, r, t.cfg.To == UTF16LE)
	}
	if t.cfg.Limit > 0 && len(t.out) > t.cfg.Limit {
		t.out = t.out[:old]
		return ErrOutputLimit
	}
	return nil
}

func (t *Transcoder) unit(kind error, off int64, n int) error {
	t.stats.Invalid++
	t.stats.BadBytes += int64(n)
	if t.cfg.OnUnit != nil {
		t.cfg.OnUnit(kind, off, n)
	}
	if t.cfg.Strict {
		return &UnitError{Kind: kind, Offset: off, Len: n}
	}
	return t.emit(scalar.Replacement)
}

// Close finalizes the stream.
func (t *Transcoder) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	if len(t.bom) > 0 { // partial BOM at EOF: parse its bytes normally
		t.bomDone = true
		t.replay = append(t.replay, t.bom...)
		t.bom = nil
	}
	if len(t.replay) > 0 {
		if _, err := t.Write(nil); err != nil {
			return err
		}
	}
	if len(t.pend) == 0 {
		return nil
	}
	off := t.prefix - int64(len(t.pend))
	n := len(t.pend)
	t.pend = nil
	kind := ErrTruncated
	if t.cfg.Interior {
		kind = ErrInvalid
	}
	if err := t.unit(kind, off, n); err != nil {
		t.terminal = err
		return err
	}
	return nil
}

// probe consumes bytes of a possible leading BOM. It returns bytes
// consumed, whether probing ended (caller continues normal decoding on
// the rest), and any error. Only the bytes at the very start of the
// whole stream are considered; later BOM-like units stay ordinary.
func (t *Transcoder) probe(p []byte) (int, bool, error) {
	if t.cfg.From == UTF8 {
		want := []byte{0xEF, 0xBB, 0xBF}
		i := 0
		for i < len(p) {
			j := len(t.bom)
			if p[i] != want[j] {
				t.bomDone = true
				t.replay = append(append(t.replay, t.bom...), p[i])
				t.bom = nil
				return i + 1, true, nil
			}
			t.bom = append(t.bom, p[i])
			i++
			if len(t.bom) == 3 {
				t.bomDone = true
				e := t.keepBOM(3)
				t.bom = nil
				return i, true, e
			}
		}
		return i, false, nil
	}
	// UTF-16: 2 bytes detect FF FE / FE FF; otherwise replay both.
	i := 0
	for len(t.bom) < 2 && i < len(p) {
		t.bom = append(t.bom, p[i])
		i++
	}
	if len(t.bom) < 2 {
		return i, false, nil
	}
	t.bomDone = true
	b0, b1 := t.bom[0], t.bom[1]
	t.bom = nil
	switch {
	case b0 == 0xFE && b1 == 0xFF:
		t.little = false
	case b0 == 0xFF && b1 == 0xFE:
		t.little = true
	default:
		t.replay = append(t.replay, b0, b1)
		return i, true, nil
	}
	return i, true, t.keepBOM(2)
}

func (t *Transcoder) keepBOM(n int) error {
	t.stats.Consumed += int64(n)
	if t.cfg.KeepBOM {
		t.stats.Runes++
		return t.emit(0xFEFF)
	}
	t.stats.BOMBytes += int64(n)
	return nil
}
