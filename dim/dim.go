// Package dim defines the key of a three-dimensional CUBE cell.
//
// Each dimension is either a concrete string value (which may legitimately
// be "") or the ALL wildcard. ALL is represented by an explicit boolean
// sentinel so it can never collide with the concrete value "".
package dim

// Key identifies one CUBE cell. For each dimension the XAll flag decides
// between ALL (true) and the concrete string XVal (false). XVal is ignored
// when XAll is true. The empty string "" is a valid concrete value.
type Key struct {
	AVal, BVal, CVal string
	AAll, BAll, CAll bool
}

// All returns the fully wildcarded key (*,*,*), the unique level-0 cell.
func All() Key { return Key{AAll: true, BAll: true, CAll: true} }

// Level returns the number of concrete (non-ALL) dimensions, always 0..3.
func (k Key) Level() int {
	n := 3
	if k.AAll {
		n--
	}
	if k.BAll {
		n--
	}
	if k.CAll {
		n--
	}
	return n
}

// Cells enumerates the exactly 8 cell keys a fact (a,b,c) belongs to, one
// per mask m in 0..7: bit i set means dimension i takes the fact's
// concrete value, bit i clear means ALL. Bit 0 is A, bit 1 is B, bit 2 is C.
//
// The level distribution of the 8 keys is always 1 level-0, 3 level-1,
// 3 level-2 and 1 level-3, independent of the fact's values.
func Cells(a, b, c string) [8]Key {
	var ks [8]Key
	for m := 0; m < 8; m++ {
		// Canonical key: an ALL dimension always carries the empty XVal so
		// that equal cells compare equal regardless of the fact's values.
		k := Key{
			AAll: m&1 == 0,
			BAll: m&2 == 0,
			CAll: m&4 == 0,
		}
		if !k.AAll {
			k.AVal = a
		}
		if !k.BAll {
			k.BVal = b
		}
		if !k.CAll {
			k.CVal = c
		}
		ks[m] = k
	}
	return ks
}
