package ontology

import "time"

// Fact records that one property of one entity held Value during the valid
// interval [ValidFrom, ValidTo), and that the system knew this during the
// transaction interval [TxFrom, TxTo). A zero ValidTo or TxTo means
// +infinity ("still current" for TxTo).
//
// Facts are immutable once written, with one exception: an overlapping
// later write closes a fact by setting its TxTo. The valid interval and
// the value of a stored fact never change.
type Fact struct {
	Entity    string
	Property  string
	Value     any
	ValidFrom time.Time
	ValidTo   time.Time
	TxFrom    time.Time
	TxTo      time.Time
}

// visibleAt reports whether the fact is part of the system state at txAt:
// known at or before txAt and not yet superseded.
func (f *Fact) visibleAt(txAt time.Time) bool {
	return !f.TxFrom.After(txAt) && beforeTo(txAt, f.TxTo)
}

// Correction is one entry of the correction trajectory of a property at a
// fixed valid time: which value the system believed, and during which
// transaction interval it believed it.
type Correction struct {
	Value  any
	TxFrom time.Time
	TxTo   time.Time // zero means the belief is still current
}
