package byterange

// Range is a normalized, closed byte interval [Start, End],
// both zero-based and inclusive.
type Range struct {
	Start int64
	End   int64
}

// Len returns the number of bytes covered by the range.
func (r Range) Len() int64 {
	return r.End - r.Start + 1
}
