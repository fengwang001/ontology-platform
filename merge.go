package hll

// Merge combines two estimators register-wise: each register in the result is
// the maximum of the corresponding registers in a and b. Neither a nor b is
// modified. It returns a PrecisionMismatchError when the precisions differ;
// the error exposes both p values for programmatic handling.
//
// Merging an empty estimator acts as the identity: Merge(x, New(x.Precision()))
// produces a byte-identical register snapshot to x.
func Merge(a, b *Estimator) (*Estimator, error) {
	if a == nil || b == nil {
		return nil, ErrNilEstimator
	}
	if a.p != b.p {
		return nil, PrecisionMismatchError{PA: a.p, PB: b.p}
	}
	out, err := New(a.p)
	if err != nil {
		return nil, err
	}
	for i := range a.registers {
		av, bv := a.registers[i], b.registers[i]
		if bv > av {
			av = bv
		}
		out.registers[i] = av
	}
	return out, nil
}
