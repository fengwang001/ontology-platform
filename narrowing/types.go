// Package narrowing implements a flow-sensitive union-type narrowing
// analyzer for structured programs built from assignments, conditionals
// and early returns.
package narrowing

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind enumerates the normalized union member kinds.
type Kind int

const (
	KindNumber Kind = iota
	KindString
	KindNull
	KindUndefined
	KindNumberLiteral
	KindStringLiteral
	KindBooleanLiteral
	KindObject
)

// Member is a single normalized union member. It is immutable.
type Member struct {
	kind  Kind
	num   float64
	str   string
	flag  bool
	props map[string]Type // object members only
	key   string
}

// Kind returns the member kind.
func (m Member) Kind() Kind { return m.kind }

// NumberValue returns the value of a number literal member.
func (m Member) NumberValue() float64 { return m.num }

// StringValue returns the value of a string literal member.
func (m Member) StringValue() string { return m.str }

// BoolValue returns the value of a boolean literal member.
func (m Member) BoolValue() bool { return m.flag }

// ObjectProps returns the property map of an object member.
// The returned map must be treated as read-only.
func (m Member) ObjectProps() map[string]Type { return m.props }

// Key returns the canonical identity string of the member.
func (m Member) Key() string { return m.key }

func (m Member) String() string {
	switch m.kind {
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindNull:
		return "null"
	case KindUndefined:
		return "undefined"
	case KindNumberLiteral:
		return strconv.FormatFloat(m.num, 'g', -1, 64)
	case KindStringLiteral:
		return strconv.Quote(m.str)
	case KindBooleanLiteral:
		return strconv.FormatBool(m.flag)
	case KindObject:
		names := make([]string, 0, len(m.props))
		for name := range m.props {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("{ ")
		for i, name := range names {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s: %s", name, m.props[name])
		}
		b.WriteString(" }")
		return b.String()
	}
	return "?"
}

// Type is a normalized union type: a flat, deduplicated set of members
// where literal members absorbed by their atomic type have been removed.
// The empty union is the never type. Type is immutable.
type Type struct {
	members map[string]Member
	key     string
}

func memberKeyNumber() string { return "T:num" }
func memberKeyString() string { return "T:str" }
func memberKeyNull() string   { return "T:null" }
func memberKeyUndef() string  { return "T:undef" }

func memberKeyBool(b bool) string {
	if b {
		return "L:bool:t"
	}
	return "L:bool:f"
}

func normalizeNumber(num float64) float64 {
	if num == 0 {
		return 0 // collapse -0 into 0
	}
	return num
}

func objectKey(props map[string]Type) string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("O:{")
	for _, name := range names {
		b.WriteString(strconv.Quote(name))
		b.WriteString(":")
		b.WriteString(props[name].key)
		b.WriteString(";")
	}
	b.WriteString("}")
	return b.String()
}

// Number returns the atomic number type.
func Number() Type {
	return fromMembers([]Member{{kind: KindNumber, key: memberKeyNumber()}})
}

// StringType returns the atomic string type.
func StringType() Type {
	return fromMembers([]Member{{kind: KindString, key: memberKeyString()}})
}

// Null returns the null type.
func Null() Type {
	return fromMembers([]Member{{kind: KindNull, key: memberKeyNull()}})
}

// Undefined returns the undefined type.
func Undefined() Type {
	return fromMembers([]Member{{kind: KindUndefined, key: memberKeyUndef()}})
}

// Boolean returns the boolean type, defined as the union of the two
// boolean literals true and false.
func Boolean() Type {
	return UnionOf(BooleanLiteral(true), BooleanLiteral(false))
}

// NumberLiteral returns the literal type of a single number.
func NumberLiteral(num float64) Type {
	num = normalizeNumber(num)
	key := "L:num:" + strconv.FormatFloat(num, 'g', -1, 64)
	return fromMembers([]Member{{kind: KindNumberLiteral, num: num, key: key}})
}

// StringLiteral returns the literal type of a single string.
func StringLiteral(str string) Type {
	key := "L:str:" + strconv.Quote(str)
	return fromMembers([]Member{{kind: KindStringLiteral, str: str, key: key}})
}

// BooleanLiteral returns the literal type of a single boolean.
func BooleanLiteral(flag bool) Type {
	return fromMembers([]Member{{kind: KindBooleanLiteral, flag: flag, key: memberKeyBool(flag)}})
}

// Object returns an object type from a property map. The map is copied;
// duplicate names are impossible at this layer (see TypeExpr for the
// validating caller-facing syntax).
func Object(props map[string]Type) Type {
	copied := make(map[string]Type, len(props))
	for name, typ := range props {
		copied[name] = typ
	}
	return fromMembers([]Member{{kind: KindObject, props: copied, key: objectKey(copied)}})
}

// Never returns the empty union (the never type).
func Never() Type { return fromMembers(nil) }

// fromMembers normalizes a member list into a Type: duplicates are
// removed and literals absorbed by their atomic type are dropped.
func fromMembers(members []Member) Type {
	set := make(map[string]Member, len(members))
	for _, m := range members {
		set[m.key] = m
	}
	if _, ok := set[memberKeyNumber()]; ok {
		for key, m := range set {
			if m.kind == KindNumberLiteral {
				delete(set, key)
			}
		}
	}
	if _, ok := set[memberKeyString()]; ok {
		for key, m := range set {
			if m.kind == KindStringLiteral {
				delete(set, key)
			}
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return Type{members: set, key: "U[" + strings.Join(keys, "|") + "]"}
}

// UnionOf flattens and normalizes the union of the given types.
func UnionOf(types ...Type) Type {
	var members []Member
	for _, typ := range types {
		for _, m := range typ.members {
			members = append(members, m)
		}
	}
	return fromMembers(members)
}

// IsNever reports whether the type is the empty union.
func (t Type) IsNever() bool { return len(t.members) == 0 }

// Equal reports type equality: set equality of normalized members.
func (t Type) Equal(other Type) bool { return t.key == other.key }

// Key returns the canonical identity string of the normalized type.
func (t Type) Key() string { return t.key }

// Members returns the normalized members in deterministic (key) order.
func (t Type) Members() []Member {
	keys := make([]string, 0, len(t.members))
	for key := range t.members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Member, 0, len(keys))
	for _, key := range keys {
		out = append(out, t.members[key])
	}
	return out
}

// ContainsMember reports whether a normalized member semantically
// belongs to the type: it is either a member itself, or a literal
// absorbed by the corresponding atomic member.
func (t Type) ContainsMember(m Member) bool {
	if _, ok := t.members[m.key]; ok {
		return true
	}
	switch m.kind {
	case KindNumberLiteral:
		_, ok := t.members[memberKeyNumber()]
		return ok
	case KindStringLiteral:
		_, ok := t.members[memberKeyString()]
		return ok
	}
	return false
}

// ContainsType reports whether every normalized member of other
// belongs to t.
func (t Type) ContainsType(other Type) bool {
	for _, m := range other.members {
		if !t.ContainsMember(m) {
			return false
		}
	}
	return true
}

func (t Type) String() string {
	if t.IsNever() {
		return "never"
	}
	members := t.Members()
	parts := make([]string, 0, len(members))
	for _, m := range members {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, " | ")
}
