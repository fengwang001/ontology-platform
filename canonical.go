package ontology

import (
	"fmt"
	"strings"
)

func canonicalType(t Type) (Type, error) {
	return canonicalTypeSeen(t, map[*Type]bool{})
}

func canonicalTypeSeen(t Type, seen map[*Type]bool) (Type, error) {
	switch t.Kind {
	case KindNamed:
		if t.Name == "" {
			return Type{}, errorf(ErrParameters, "named type must have a name")
		}
		return Type{Kind: KindNamed, Name: t.Name}, nil
	case KindAlias:
		if t.AliasTo == nil {
			return Type{}, errorf(ErrParameters, "alias %q has no target", t.Name)
		}
		if seen[t.AliasTo] {
			return Type{}, errorf(ErrParameters, "alias cycle at %q", t.Name)
		}
		seen[t.AliasTo] = true
		target, err := canonicalTypeSeen(*t.AliasTo, seen)
		seen[t.AliasTo] = false
		if err != nil {
			return Type{}, err
		}
		return target, nil
	case KindStruct:
		fields := make([]Field, len(t.Fields))
		for i, field := range t.Fields {
			if field.Name == "" {
				return Type{}, errorf(ErrParameters, "struct field %d has no name", i)
			}
			fieldType, err := canonicalTypeSeen(field.Type, seen)
			if err != nil {
				return Type{}, err
			}
			fields[i] = Field{Name: field.Name, Type: fieldType}
		}
		return Type{Kind: KindStruct, Fields: fields}, nil
	case KindInstance:
		if t.Def == "" {
			return Type{}, errorf(ErrParameters, "instantiated type has no definition")
		}
		args := make([]Type, len(t.Args))
		for i, arg := range t.Args {
			canonicalArg, err := canonicalTypeSeen(arg, seen)
			if err != nil {
				return Type{}, err
			}
			args[i] = canonicalArg
		}
		return Type{Kind: KindInstance, Def: t.Def, Args: args}, nil
	default:
		return Type{}, errorf(ErrParameters, "unknown type kind %d", t.Kind)
	}
}

func canonicalArgs(args []Type) ([]Type, error) {
	result := make([]Type, len(args))
	for i, arg := range args {
		canonicalArg, err := canonicalType(arg)
		if err != nil {
			return nil, err
		}
		result[i] = canonicalArg
	}
	return result, nil
}

func instanceKey(def string, args []Type) string {
	var b strings.Builder
	b.WriteString("def=")
	appendEncodedString(&b, def)
	b.WriteString(";args=[")
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		appendTypeKey(&b, arg)
	}
	b.WriteByte(']')
	return b.String()
}

func appendTypeKey(b *strings.Builder, t Type) {
	switch t.Kind {
	case KindNamed:
		b.WriteString("named:")
		appendEncodedString(b, t.Name)
	case KindStruct:
		b.WriteString("struct:{")
		for i, field := range t.Fields {
			if i > 0 {
				b.WriteByte(',')
			}
			appendEncodedString(b, field.Name)
			b.WriteByte(':')
			appendTypeKey(b, field.Type)
		}
		b.WriteByte('}')
	case KindInstance:
		b.WriteString("instance:")
		appendEncodedString(b, t.Def)
		b.WriteString(":[")
		for i, arg := range t.Args {
			if i > 0 {
				b.WriteByte(',')
			}
			appendTypeKey(b, arg)
		}
		b.WriteByte(']')
	default:
		b.WriteString("invalid:")
	}
}

func appendEncodedString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

func sameType(left, right Type) bool {
	return typeKey(left) == typeKey(right)
}

func typeKey(t Type) string {
	var b strings.Builder
	appendTypeKey(&b, t)
	return b.String()
}

func copyArgs(args []Type) []Type {
	if len(args) == 0 {
		return nil
	}
	copied := make([]Type, len(args))
	copy(copied, args)
	return copied
}

func typeDebug(t Type) string {
	return fmt.Sprintf("%v", t)
}

func canonicalizeArgs(args []Type) ([]Type, error) {
	return canonicalArgs(args)
}
