package argv

// Result holds the outcome of a successful Parse.
type Result struct {
	bools    map[string]bool
	strings  map[string]string
	set      map[string]bool
	defaults map[string]string
	operands []string
}

// Bool reports the value of a Bool flag. Unset flags report false.
func (r *Result) Bool(long string) bool {
	return r.bools[long]
}

// String reports the value of a String flag. Unset flags report
// their declared Default.
func (r *Result) String(long string) string {
	if v, ok := r.strings[long]; ok {
		return v
	}
	return r.defaults[long]
}

// WasSet reports whether the flag was given explicitly on the
// command line, as opposed to falling back to its Default.
func (r *Result) WasSet(long string) bool {
	return r.set[long]
}

// Operands returns the positional arguments in their original order.
func (r *Result) Operands() []string {
	out := make([]string, len(r.operands))
	copy(out, r.operands)
	return out
}
