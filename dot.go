package ontology

// Dot computes the dot product of two sparse vectors with a single
// two-pointer merge. The vectors are never expanded to dense arrays
// and never modified.
//
// Products at matching indexes are accumulated with Neumaier
// compensated summation, so the result stays accurate when magnitudes
// are wildly mixed and depends only on the merged term sequence —
// reshuffling and re-sorting an input yields bit-identical results.
//
// It returns a decidable error (*OrderError, *NaNError, *InfError)
// for malformed input instead of ever handing back a NaN.
func Dot(a, b SparseVector) (float64, Stats, error) {
	var st Stats
	if err := validate(a, First, &st); err != nil {
		return 0, st, err
	}
	if err := validate(b, Second, &st); err != nil {
		return 0, st, err
	}

	sum, comp := 0.0, 0.0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		st.Steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			sum, comp = neumaier(sum, comp, a[i].Value*b[j].Value)
			i++
			j++
		}
	}
	return sum + comp, st, nil
}
