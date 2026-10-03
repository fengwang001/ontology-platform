// Package resolver implements a type-class instance resolver with
// overlapping instances and an incrementally invalidated resolution cache.
package resolver

import (
	"strconv"
	"strings"
)

const (
	MinDepthLimit = 1
	MaxDepthLimit = 64

	MaxTypeDepth = 16
	MaxConArgs   = 4
	MaxNameBytes = 32
	MaxVarIndex  = 7
	MaxContext   = 4
	MaxInstances = 200
)

// Type is a constructor application Con(name, args) or a variable Var(i).
// A variable is represented with IsVar == true; constructor fields are
// ignored for variables.
type Type struct {
	IsVar bool
	Var   int
	Name  string
	Args  []*Type
}

// Con builds a constructor application type.
func Con(name string, args ...*Type) *Type {
	return &Type{Name: name, Args: args}
}

// Var builds a variable type with index i.
func Var(i int) *Type {
	return &Type{IsVar: true, Var: i}
}

// Constraint is a (trait, type) pair.
type Constraint struct {
	Trait string
	Type  *Type
}

// String renders the canonical text of a type: a nullary constructor is
// written as its name, otherwise the name is followed by the arguments in
// angle brackets separated by commas. Variables (which never occur in
// ground types) are rendered as "?i".
func (t *Type) String() string {
	if t.IsVar {
		return "?" + strconv.Itoa(t.Var)
	}
	if len(t.Args) == 0 {
		return t.Name
	}
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteByte('<')
	for i, a := range t.Args {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(a.String())
	}
	b.WriteByte('>')
	return b.String()
}

// Depth of a type: a nullary constructor has depth 1, otherwise it is
// 1 plus the maximum depth of the arguments. Variables count as 1.
func (t *Type) Depth() int {
	if t.IsVar || len(t.Args) == 0 {
		return 1
	}
	maxDepth := 0
	for _, a := range t.Args {
		if d := a.Depth(); d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth + 1
}

// IsGround reports whether the type contains no variables.
func (t *Type) IsGround() bool {
	if t.IsVar {
		return false
	}
	for _, a := range t.Args {
		if !a.IsGround() {
			return false
		}
	}
	return true
}

func cloneType(t *Type) *Type {
	if t.IsVar {
		return &Type{IsVar: true, Var: t.Var}
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = cloneType(a)
	}
	return &Type{Name: t.Name, Args: args}
}

func typeEqual(a, b *Type) bool {
	if a.IsVar != b.IsVar {
		return false
	}
	if a.IsVar {
		return a.Var == b.Var
	}
	if a.Name != b.Name || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !typeEqual(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return true
}

// collectVars gathers the variable indices occurring in t.
func collectVars(t *Type, set map[int]bool) {
	if t.IsVar {
		set[t.Var] = true
		return
	}
	for _, a := range t.Args {
		collectVars(a, set)
	}
}

// normalize renumbers the variables of t by preorder of first occurrence,
// starting from 0. Two heads are duplicates iff their normal forms are
// structurally equal.
func normalize(t *Type) *Type {
	mapping := map[int]int{}
	var rec func(x *Type) *Type
	rec = func(x *Type) *Type {
		if x.IsVar {
			id, ok := mapping[x.Var]
			if !ok {
				id = len(mapping)
				mapping[x.Var] = id
			}
			return &Type{IsVar: true, Var: id}
		}
		args := make([]*Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = rec(a)
		}
		return &Type{Name: x.Name, Args: args}
	}
	return rec(t)
}

func validateName(kind, name string) error {
	if len(name) == 0 {
		return &Error{Kind: ErrInvalidParam, Detail: kind + " name is empty"}
	}
	if len(name) > MaxNameBytes {
		return &Error{Kind: ErrInvalidParam, Detail: kind + " name exceeds 32 bytes"}
	}
	return nil
}

// validateType checks the structural well-formedness of a type: names are
// 1..32 bytes, constructors have at most 4 arguments, and variable indices
// are in 0..7.
func validateType(t *Type) error {
	if t == nil {
		return &Error{Kind: ErrInvalidParam, Detail: "nil type"}
	}
	if t.IsVar {
		if t.Var < 0 || t.Var > MaxVarIndex {
			return &Error{Kind: ErrInvalidParam, Detail: "variable index out of range 0..7"}
		}
		return nil
	}
	if err := validateName("constructor", t.Name); err != nil {
		return err
	}
	if len(t.Args) > MaxConArgs {
		return &Error{Kind: ErrInvalidParam, Detail: "constructor has more than 4 arguments"}
	}
	for _, a := range t.Args {
		if err := validateType(a); err != nil {
			return err
		}
	}
	return nil
}

// validateGround checks that t is a well-formed ground type of depth at
// most MaxTypeDepth.
func validateGround(t *Type) error {
	if err := validateType(t); err != nil {
		return err
	}
	if !t.IsGround() {
		return &Error{Kind: ErrInvalidParam, Detail: "ground type contains a variable"}
	}
	if t.Depth() > MaxTypeDepth {
		return &Error{Kind: ErrInvalidParam, Detail: "ground type depth exceeds 16"}
	}
	return nil
}
