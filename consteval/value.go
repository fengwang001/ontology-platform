package consteval

import (
	"fmt"
	"math/big"
)

// Const is an evaluated constant: either untyped (Type() == TypeNone,
// carrying only a kind) or typed. Untyped integers and rationals use
// arbitrary precision. Const values are immutable after creation; all
// accessors that expose big numbers return copies.
type Const struct {
	typ  Type
	kind Kind
	i    *big.Int // untyped int
	r    *big.Rat // untyped rational
	si   int64    // typed signed integer
	ui   uint64   // typed unsigned integer
	f    float64  // typed float64
	b    bool     // bool (typed or untyped)
	s    string   // string (typed or untyped)
}

func untypedInt(v *big.Int) *Const  { return &Const{typ: TypeNone, kind: KindInt, i: v} }
func untypedRat(v *big.Rat) *Const  { return &Const{typ: TypeNone, kind: KindRational, r: v} }
func untypedBool(v bool) *Const     { return &Const{typ: TypeNone, kind: KindBool, b: v} }
func untypedString(v string) *Const { return &Const{typ: TypeNone, kind: KindString, s: v} }

func typedSigned(t Type, v int64) *Const    { return &Const{typ: t, kind: KindInt, si: v} }
func typedUnsigned(t Type, v uint64) *Const { return &Const{typ: t, kind: KindInt, ui: v} }
func typedFloat(v float64) *Const           { return &Const{typ: TypeFloat64, kind: KindRational, f: v} }
func typedBool(v bool) *Const               { return &Const{typ: TypeBool, kind: KindBool, b: v} }
func typedString(v string) *Const           { return &Const{typ: TypeString, kind: KindString, s: v} }

// Untyped reports whether the constant is untyped.
func (c *Const) Untyped() bool { return c.typ == TypeNone }

// Kind returns the constant's kind.
func (c *Const) Kind() Kind { return c.kind }

// Type returns the constant's concrete type, or TypeNone when untyped.
func (c *Const) Type() Type { return c.typ }

// Int returns a copy of the untyped integer value.
func (c *Const) Int() *big.Int { return new(big.Int).Set(c.i) }

// Rat returns a copy of the untyped rational value.
func (c *Const) Rat() *big.Rat { return new(big.Rat).Set(c.r) }

// Int64 returns the value of a typed signed integer constant.
func (c *Const) Int64() int64 { return c.si }

// Uint64 returns the value of a typed unsigned integer constant.
func (c *Const) Uint64() uint64 { return c.ui }

// Float64 returns the value of a typed float64 constant.
func (c *Const) Float64() float64 { return c.f }

// Bool returns the value of a bool constant.
func (c *Const) Bool() bool { return c.b }

// Str returns the value of a string constant.
func (c *Const) Str() string { return c.s }

func (c *Const) String() string {
	prefix := c.typ.String()
	switch {
	case c.typ == TypeNone && c.kind == KindInt:
		return fmt.Sprintf("untyped int %s", c.i)
	case c.typ == TypeNone && c.kind == KindRational:
		return fmt.Sprintf("untyped rational %s", c.r.RatString())
	case c.typ == TypeNone && c.kind == KindBool:
		return fmt.Sprintf("untyped bool %t", c.b)
	case c.typ == TypeNone && c.kind == KindString:
		return fmt.Sprintf("untyped string %q", c.s)
	case c.typ.IsInt() && c.typ.Signed():
		return fmt.Sprintf("%s %d", prefix, c.si)
	case c.typ.IsInt():
		return fmt.Sprintf("%s %d", prefix, c.ui)
	case c.typ == TypeFloat64:
		return fmt.Sprintf("float64 %v", c.f)
	case c.typ == TypeBool:
		return fmt.Sprintf("bool %t", c.b)
	case c.typ == TypeString:
		return fmt.Sprintf("string %q", c.s)
	}
	return "invalid const"
}

// category maps the constant onto its operational category.
func (c *Const) category() category {
	if c.typ == TypeNone {
		switch c.kind {
		case KindInt:
			return catInt
		case KindRational:
			return catRat
		case KindBool:
			return catBool
		case KindString:
			return catString
		}
		return catInvalid
	}
	switch {
	case c.typ.IsInt():
		return catInt
	case c.typ == TypeFloat64:
		return catFloat
	case c.typ == TypeBool:
		return catBool
	case c.typ == TypeString:
		return catString
	}
	return catInvalid
}

// isZero reports whether the constant is a numeric zero.
func (c *Const) isZero() bool {
	switch c.category() {
	case catInt:
		if c.typ == TypeNone {
			return c.i.Sign() == 0
		}
		if c.typ.Signed() {
			return c.si == 0
		}
		return c.ui == 0
	case catRat:
		return c.r.Sign() == 0
	case catFloat:
		return c.f == 0
	}
	return false
}

// intValue returns the exact integer value of an integer-category
// constant (untyped int or typed integer).
func (c *Const) intValue() *big.Int {
	if c.typ == TypeNone {
		return new(big.Int).Set(c.i)
	}
	if c.typ.Signed() {
		return big.NewInt(c.si)
	}
	return new(big.Int).SetUint64(c.ui)
}

// ratValue returns the exact rational value of an untyped numeric
// constant. It must not be called on typed or non-numeric constants.
func (c *Const) ratValue() *big.Rat {
	if c.kind == KindInt {
		return new(big.Rat).SetInt(c.i)
	}
	return new(big.Rat).Set(c.r)
}
