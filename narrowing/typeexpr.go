package narrowing

import (
	"math"
	"strconv"
)

// AltKind enumerates the alternatives of a caller-facing type expression.
type AltKind int

const (
	AltNumber AltKind = iota
	AltString
	AltBoolean
	AltNull
	AltUndefined
	AltNumberLiteral
	AltStringLiteral
	AltBooleanLiteral
	AltObject
)

// PropExpr is one property of an object type expression. Properties are
// kept as an ordered list so that duplicate names can be detected and
// reported as malformed input.
type PropExpr struct {
	Name string
	Type TypeExpr
}

// AltExpr is one alternative of a union type expression.
type AltExpr struct {
	Kind  AltKind
	Num   float64    // AltNumberLiteral
	Str   string     // AltStringLiteral
	Bool  bool       // AltBooleanLiteral
	Props []PropExpr // AltObject
}

// TypeExpr is the caller-facing syntax for a (possibly union) type.
// An empty Alts slice denotes the never type.
type TypeExpr struct {
	Alts []AltExpr
}

// Convenience constructors for type expressions.
func NumberExpr() TypeExpr    { return TypeExpr{Alts: []AltExpr{{Kind: AltNumber}}} }
func StringExpr() TypeExpr    { return TypeExpr{Alts: []AltExpr{{Kind: AltString}}} }
func BooleanExpr() TypeExpr   { return TypeExpr{Alts: []AltExpr{{Kind: AltBoolean}}} }
func NullExpr() TypeExpr      { return TypeExpr{Alts: []AltExpr{{Kind: AltNull}}} }
func UndefinedExpr() TypeExpr { return TypeExpr{Alts: []AltExpr{{Kind: AltUndefined}}} }
func NeverExpr() TypeExpr     { return TypeExpr{} }
func NumLitExpr(v float64) TypeExpr {
	return TypeExpr{Alts: []AltExpr{{Kind: AltNumberLiteral, Num: v}}}
}
func StrLitExpr(s string) TypeExpr {
	return TypeExpr{Alts: []AltExpr{{Kind: AltStringLiteral, Str: s}}}
}
func BoolLitExpr(b bool) TypeExpr {
	return TypeExpr{Alts: []AltExpr{{Kind: AltBooleanLiteral, Bool: b}}}
}
func ObjectExpr(props ...PropExpr) TypeExpr {
	return TypeExpr{Alts: []AltExpr{{Kind: AltObject, Props: props}}}
}

// Prop builds one object property expression.
func Prop(name string, typ TypeExpr) PropExpr { return PropExpr{Name: name, Type: typ} }

// UnionExpr flattens several type expressions into one union expression.
func UnionExpr(exprs ...TypeExpr) TypeExpr {
	var alts []AltExpr
	for _, e := range exprs {
		alts = append(alts, e.Alts...)
	}
	return TypeExpr{Alts: alts}
}

// normalize validates the expression and folds it into a semantic Type.
// Duplicate property names inside a single object alternative and NaN
// number literals are malformed input.
func (e TypeExpr) normalize() (Type, error) {
	var types []Type
	for _, alt := range e.Alts {
		switch alt.Kind {
		case AltNumber:
			types = append(types, Number())
		case AltString:
			types = append(types, StringType())
		case AltBoolean:
			types = append(types, Boolean())
		case AltNull:
			types = append(types, Null())
		case AltUndefined:
			types = append(types, Undefined())
		case AltNumberLiteral:
			if math.IsNaN(alt.Num) {
				return Type{}, errMalformed("number literal is NaN")
			}
			types = append(types, NumberLiteral(alt.Num))
		case AltStringLiteral:
			types = append(types, StringLiteral(alt.Str))
		case AltBooleanLiteral:
			types = append(types, BooleanLiteral(alt.Bool))
		case AltObject:
			props := make(map[string]Type, len(alt.Props))
			for _, p := range alt.Props {
				if _, dup := props[p.Name]; dup {
					return Type{}, errMalformed("duplicate property name " + strconv.Quote(p.Name))
				}
				pt, err := p.Type.normalize()
				if err != nil {
					return Type{}, err
				}
				props[p.Name] = pt
			}
			types = append(types, Object(props))
		default:
			return Type{}, errMalformed("unknown type alternative kind")
		}
	}
	return UnionOf(types...), nil
}
