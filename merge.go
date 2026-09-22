package ontology

import "fmt"

// PMismatchError reports an attempt to merge estimators built with
// different precisions. Both p values are exposed so callers can
// decide programmatically.
type PMismatchError struct {
	Pa, Pb uint8
}

func (e *PMismatchError) Error() string {
	return fmt.Sprintf("ontology: cannot merge estimators with different p (%d vs %d)", e.Pa, e.Pb)
}

// Merge returns a new estimator whose registers are the element-wise
// maximum of a and b. Neither source is modified. Merging with an
// empty estimator is the identity operation. If the precisions
// differ, Merge returns a *PMismatchError naming both p values.
func Merge(a, b *HLL) (*HLL, error) {
	if a.p != b.p {
		return nil, &PMismatchError{Pa: a.p, Pb: b.p}
	}
	out := &HLL{p: a.p, reg: make([]uint8, len(a.reg))}
	for i := range out.reg {
		out.reg[i] = max(a.reg[i], b.reg[i])
	}
	return out, nil
}
