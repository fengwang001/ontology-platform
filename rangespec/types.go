package rangespec

// Interval is a byte interval using inclusive Start and inclusive End offsets.
type Interval struct {
	Start int64
	End   int64
}

// SyntaxError identifies a malformed Range header by byte offset.
type SyntaxError struct {
	Offset int
}

func (e SyntaxError) Error() string { return "invalid Range header syntax" }
