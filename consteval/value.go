package consteval

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// maxUntypedIntBits bounds the magnitude of untyped integers: any
// intermediate result whose absolute value needs more bits is "constant
// too large".
const maxUntypedIntBits = 512

// maxShiftCount bounds the right operand of shift operators.
const maxShiftCount = 1000

// Value is an immutable constant. Untyped values (typ == NoType) carry a
// kind; typed values carry both. The pointed-to big.Int / big.Rat are never
// mutated after construction, so Values are safe to share across goroutines.
type Value struct {
	typ  Type
	kind Kind
	i    *big.Int // IntKind (untyped or sized integer types)
	r    *big.Rat // RatKind (untyped only)
	f    float64  // Float64
	b    bool     // BoolKind
	s    string   // StringKind
}

// Constructors for untyped values.

// UntypedInt builds an untyped integer constant.
func UntypedInt(i *big.Int) (Value, error) {
	v := Value{typ: NoType, kind: IntKind, i: new(big.Int).Set(i)}
	if err := checkUntypedInt(v.i); err != nil {
		return Value{}, err
	}
	return v, nil
}

// MustUntypedInt is UntypedInt for values known to be small (test helper).
func MustUntypedInt(x int64) Value {
	return Value{typ: NoType, kind: IntKind, i: big.NewInt(x)}
}

// UntypedRat builds an untyped rational constant. The kind stays RatKind
// even when the value happens to be integral.
func UntypedRat(r *big.Rat) Value {
	return Value{typ: NoType, kind: RatKind, r: new(big.Rat).Set(r)}
}

// UntypedBool builds an untyped boolean constant.
func UntypedBool(b bool) Value {
	return Value{typ: NoType, kind: BoolKind, b: b}
}

// UntypedString builds an untyped string constant.
func UntypedString(s string) Value {
	return Value{typ: NoType, kind: StringKind, s: s}
}

// Accessors.

// Type returns the concrete type, or NoType for untyped constants.
func (v Value) Type() Type { return v.typ }

// KindOf returns the value kind.
func (v Value) KindOf() Kind { return v.kind }

// EffectiveType returns the concrete type of the constant, applying the
// default type for untyped constants (int64 / float64 / bool / string).
func (v Value) EffectiveType() Type {
	if v.typ != NoType {
		return v.typ
	}
	return DefaultType(v.kind)
}

// Int returns the integer value (IntKind only).
func (v Value) Int() *big.Int { return new(big.Int).Set(v.i) }

// Rat returns the exact rational value (IntKind and untyped RatKind).
func (v Value) Rat() *big.Rat {
	if v.kind == IntKind {
		return new(big.Rat).SetInt(v.i)
	}
	return new(big.Rat).Set(v.r)
}

// Float returns the float64 value (Float64 type only).
func (v Value) Float() float64 { return v.f }

// Bool returns the boolean value (BoolKind only).
func (v Value) Bool() bool { return v.b }

// Str returns the string value (StringKind only).
func (v Value) Str() string { return v.s }

func (v Value) String() string {
	t := ""
	if v.typ != NoType {
		t = v.typ.String() + " "
	}
	switch v.kind {
	case IntKind:
		return fmt.Sprintf("%s%s", t, v.i.String())
	case RatKind:
		if v.typ == Float64 {
			return fmt.Sprintf("%s%g", t, v.f)
		}
		return fmt.Sprintf("%s%s", t, v.r.RatString())
	case BoolKind:
		return fmt.Sprintf("%s%t", t, v.b)
	case StringKind:
		return fmt.Sprintf("%s%q", t, v.s)
	}
	return "<invalid>"
}

// checkUntypedInt enforces the 512-bit magnitude limit on untyped integers.
func checkUntypedInt(i *big.Int) error {
	if i.BitLen() > maxUntypedIntBits {
		return errf(ErrConstTooLarge, "untyped integer %s... exceeds %d bits",
			i.String()[:min(20, len(i.String()))], maxUntypedIntBits)
	}
	return nil
}

// intRange returns [lo, hi] for a sized integer type.
func intRange(t Type) (lo, hi *big.Int) {
	w := t.Width()
	if t.IsSigned() {
		hi = new(big.Int).Lsh(big.NewInt(1), w-1)
		lo = new(big.Int).Neg(new(big.Int).Set(hi))
		hi.Sub(hi, big.NewInt(1))
		return lo, hi
	}
	hi = new(big.Int).Lsh(big.NewInt(1), w)
	hi.Sub(hi, big.NewInt(1))
	return big.NewInt(0), hi
}

// ratToFloat64 rounds an exact rational to the nearest float64, ties to
// even. A result of infinity (from a finite input) is reported as overflow.
func ratToFloat64(r *big.Rat) (float64, error) {
	f, _ := r.Float64()
	if math.IsInf(f, 0) {
		return 0, errf(ErrOverflow, "value %s rounds to infinity", r.FloatString(10))
	}
	return f, nil
}

// convert changes an untyped value to a concrete type, enforcing
// representability: integer types require an exactly integral value in
// range (truncation and overflow are distinguished); float64 rounds to
// nearest, ties to even, and rejects results rounded to infinity.
func convert(v Value, t Type) (Value, error) {
	if v.typ != NoType {
		if v.typ == t {
			return v, nil
		}
		return Value{}, errf(ErrTypeMismatch, "cannot convert %s to %s", v.typ, t)
	}
	switch {
	case t.IsInt():
		var i *big.Int
		switch v.kind {
		case IntKind:
			i = v.i
		case RatKind:
			if !v.r.IsInt() {
				return Value{}, errf(ErrTruncation,
					"cannot convert non-integral %s to %s", v.r.RatString(), t)
			}
			i = new(big.Int).Set(v.r.Num())
		default:
			return Value{}, errf(ErrTypeMismatch, "cannot convert %s to %s", v.kind, t)
		}
		lo, hi := intRange(t)
		if i.Cmp(lo) < 0 || i.Cmp(hi) > 0 {
			return Value{}, errf(ErrOverflow, "value %s out of range for %s", i.String(), t)
		}
		return Value{typ: t, kind: IntKind, i: new(big.Int).Set(i)}, nil
	case t == Float64:
		switch v.kind {
		case IntKind:
			f, err := ratToFloat64(new(big.Rat).SetInt(v.i))
			if err != nil {
				return Value{}, err
			}
			return Value{typ: Float64, kind: RatKind, f: f}, nil
		case RatKind:
			f, err := ratToFloat64(v.r)
			if err != nil {
				return Value{}, err
			}
			return Value{typ: Float64, kind: RatKind, f: f}, nil
		default:
			return Value{}, errf(ErrTypeMismatch, "cannot convert %s to %s", v.kind, t)
		}
	case t == Bool:
		if v.kind != BoolKind {
			return Value{}, errf(ErrTypeMismatch, "cannot convert %s to %s", v.kind, t)
		}
		return Value{typ: Bool, kind: BoolKind, b: v.b}, nil
	case t == String:
		if v.kind != StringKind {
			return Value{}, errf(ErrTypeMismatch, "cannot convert %s to %s", v.kind, t)
		}
		return Value{typ: String, kind: StringKind, s: v.s}, nil
	}
	return Value{}, errf(ErrInvalidArgument, "unknown target type %v", int(t))
}

// compareStrings compares by byte order.
func compareStrings(a, b string) int {
	return strings.Compare(a, b)
}
