// Package pred defines the conjunctive predicate language, integer-interval
// semantics, containment (P ⊆ Vp) and residual-predicate computation.
//
// The language is conjunction-only: at most one atom per column, no OR/NOT.
// region supports equality only; amount/year support =, <, <=, >, >=; an
// omitted atom (Op None) means the full universe.
package pred

// Op is the operator of a single-column atom.
type Op uint8

const (
	None Op = iota // omitted: any value
	Eq             // =
	Lt             // <
	Le             // <=
	Gt             // >
	Ge             // >=
)

// Atom is one column predicate. Str is used by region equality only.
type Atom struct {
	Op  Op
	Int int
	Str string
}

// Pred is a conjunction of at most one atom per column.
type Pred struct {
	Region Atom
	Amount Atom
	Year   Atom
}

// RegionEq builds a region equality atom.
func RegionEq(s string) Atom { return Atom{Op: Eq, Str: s} }

// IEq, ILt, ILe, IGt, IGe build integer-column atoms.
func IEq(k int) Atom { return Atom{Op: Eq, Int: k} }
func ILt(k int) Atom { return Atom{Op: Lt, Int: k} }
func ILe(k int) Atom { return Atom{Op: Le, Int: k} }
func IGt(k int) Atom { return Atom{Op: Gt, Int: k} }
func IGe(k int) Atom { return Atom{Op: Ge, Int: k} }

// Present reports whether the atom constrains its column.
func (a Atom) Present() bool { return a.Op != None }

// Valid reports whether p is expressible in the predicate language:
// region may only be equality, integer ops must be known operators.
func Valid(p Pred) bool {
	if p.Region.Op != None && p.Region.Op != Eq {
		return false
	}
	return p.Amount.Op <= Ge && p.Year.Op <= Ge
}

// Match evaluates the conjunction against one base row.
func (p Pred) Match(region string, amount, year int) bool {
	if p.Region.Op == Eq && region != p.Region.Str {
		return false
	}
	return matchInt(p.Amount, amount) && matchInt(p.Year, year)
}

func matchInt(a Atom, v int) bool {
	switch a.Op {
	case Eq:
		return v == a.Int
	case Lt:
		return v < a.Int
	case Le:
		return v <= a.Int
	case Gt:
		return v > a.Int
	case Ge:
		return v >= a.Int
	}
	return true
}

// interval is a closed integer range; nil means unbounded.
type interval struct{ lo, hi *int }

func iv(a Atom) interval {
	v := a.Int
	switch a.Op {
	case Eq:
		return interval{&v, &v}
	case Lt:
		w := v - 1 // <k = (-∞, k-1]
		return interval{nil, &w}
	case Le:
		return interval{nil, &v}
	case Gt:
		w := v + 1 // >k = [k+1, +∞)
		return interval{&w, nil}
	case Ge:
		return interval{&v, nil}
	}
	return interval{} // None: (-∞,+∞)
}

func intContains(sub, sup Atom) bool {
	a, b := iv(sub), iv(sup)
	if a.lo == nil && b.lo != nil { // sub reaches -∞ but sup does not
		return false
	}
	if a.lo != nil && b.lo != nil && *a.lo < *b.lo {
		return false
	}
	if a.hi == nil && b.hi != nil { // sub reaches +∞ but sup does not
		return false
	}
	if a.hi != nil && b.hi != nil && *a.hi > *b.hi {
		return false
	}
	return true
}

func regionContains(sub, sup Atom) bool {
	if sup.Op == None {
		return true // universe
	}
	return sub.Op == Eq && sub.Str == sup.Str
}

// Contains reports sub ⊆ sup, column by column (invariant 2 gate).
func Contains(sub, sup Pred) bool {
	return regionContains(sub.Region, sup.Region) &&
		intContains(sub.Amount, sup.Amount) &&
		intContains(sub.Year, sup.Year)
}

// Residual returns the atoms of sub that are strictly narrower than sup,
// column by column. Call only when Contains(sub, sup) holds; together with
// sup the result is equivalent to sub on the base table (invariant 3).
func Residual(sub, sup Pred) Pred {
	var r Pred
	if regionContains(sub.Region, sup.Region) && !regionContains(sup.Region, sub.Region) {
		r.Region = sub.Region
	}
	if intContains(sub.Amount, sup.Amount) && !intContains(sup.Amount, sub.Amount) {
		r.Amount = sub.Amount
	}
	if intContains(sub.Year, sup.Year) && !intContains(sup.Year, sub.Year) {
		r.Year = sub.Year
	}
	return r
}
