package argv

// Result holds the outcome of a successful Parse.
type Result struct {
	bools    map[string]bool
	strings  map[string]string
	set      map[string]bool
	operands []string
}

// Bool reports whether the Bool flag with the given long name was
// provided. Unknown names report false.
func (r *Result) Bool(long string) bool {
	return r.bools[long]
}

// String returns the value of the String flag with the given long
// name, or its Default if it was not provided. Unknown names and
// flags without a default report "".
func (r *Result) String(long string) string {
	return r.strings[long]
}

// WasSet reports whether the flag with the given long name was
// explicitly provided on the command line, as opposed to falling
// back to its default.
func (r *Result) WasSet(long string) bool {
	return r.set[long]
}

// Operands returns the positional (non-flag) arguments in their
// original order.
func (r *Result) Operands() []string {
	out := make([]string, len(r.operands))
	copy(out, r.operands)
	return out
}
