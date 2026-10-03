// Package resolver implements an overlapping-instance type class instance
// resolver with an incrementally invalidated resolution cache.
package resolver

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	maxNameBytes  = 32
	maxArgs       = 4
	maxVarIndex   = 7
	maxTypeDepth  = 16
	maxContext    = 4
	maxInstances  = 200
	minDepthLimit = 1
	maxDepthLimit = 64
)

// RejectReason distinguishes why an operation was rejected.
type RejectReason int

const (
	// RejectInvalidParam: empty/overlong name, arg count out of range,
	// variable index out of range, non-ground or too-deep ground type,
	// context variable absent from the head, or depth limit out of range.
	RejectInvalidParam RejectReason = iota
	// RejectDuplicateInstance: an instance with an alpha-equivalent head
	// already exists for the same trait.
	RejectDuplicateInstance
	// RejectTooManyInstances: the instance table is full.
	RejectTooManyInstances
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidParam:
		return "invalid parameter"
	case RejectDuplicateInstance:
		return "duplicate instance"
	case RejectTooManyInstances:
		return "too many instances"
	}
	return "unknown"
}

// RejectError describes a rejected operation. Rejected operations never
// mutate any state.
type RejectError struct {
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("resolver: %s: %s", e.Reason, e.Detail)
}

func rejectf(reason RejectReason, format string, args ...any) *RejectError {
	return &RejectError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Type is a constructor application Con(name, args) or a variable Var(i).
// Values must be built with Con or Var; the zero value is invalid.
type Type struct {
	isVar bool
	name  string
	args  []Type
	vari  int
	ok    bool
}

func validName(name string) bool {
	return len(name) >= 1 && len(name) <= maxNameBytes
}

// Con builds a constructor application. The name must be a non-empty string
// of at most 32 bytes and there may be at most 4 arguments.
func Con(name string, args ...Type) (Type, error) {
	if !validName(name) {
		return Type{}, rejectf(RejectInvalidParam, "constructor name %q: must be 1..32 bytes", name)
	}
	if len(args) > maxArgs {
		return Type{}, rejectf(RejectInvalidParam, "constructor %q: %d arguments, at most %d allowed", name, len(args), maxArgs)
	}
	for _, a := range args {
		if !a.valid() {
			return Type{}, rejectf(RejectInvalidParam, "constructor %q: invalid argument type", name)
		}
	}
	cp := make([]Type, len(args))
	copy(cp, args)
	return Type{name: name, args: cp, ok: true}, nil
}

// Var builds a variable with index i in [0, 7]. Variables may only appear
// inside instance heads and context constraints.
func Var(i int) (Type, error) {
	if i < 0 || i > maxVarIndex {
		return Type{}, rejectf(RejectInvalidParam, "variable index %d: must be 0..%d", i, maxVarIndex)
	}
	return Type{isVar: true, vari: i, ok: true}, nil
}

func (t Type) valid() bool {
	if !t.ok {
		return false
	}
	if t.isVar {
		return t.vari >= 0 && t.vari <= maxVarIndex
	}
	if !validName(t.name) || len(t.args) > maxArgs {
		return false
	}
	for _, a := range t.args {
		if !a.valid() {
			return false
		}
	}
	return true
}

// IsVar reports whether t is a variable.
func (t Type) IsVar() bool { return t.ok && t.isVar }

// IsGround reports whether t contains no variables.
func (t Type) IsGround() bool {
	if !t.ok {
		return false
	}
	if t.isVar {
		return false
	}
	for _, a := range t.args {
		if !a.IsGround() {
			return false
		}
	}
	return true
}

// Depth returns 1 for a zero-argument constructor (or a variable), and
// 1 + max argument depth otherwise.
func (t Type) Depth() int {
	if !t.ok || t.isVar {
		return 1
	}
	max := 0
	for _, a := range t.args {
		if d := a.Depth(); d > max {
			max = d
		}
	}
	return max + 1
}

// String returns the canonical text: a zero-argument constructor is written
// as its name, otherwise the name followed by the arguments in angle
// brackets separated by commas. Variables are written a..h (they never
// appear in ground types).
func (t Type) String() string {
	if !t.ok {
		return "<invalid>"
	}
	if t.isVar {
		return string(rune('a' + t.vari))
	}
	if len(t.args) == 0 {
		return t.name
	}
	parts := make([]string, len(t.args))
	for i, a := range t.args {
		parts[i] = a.String()
	}
	return t.name + "<" + strings.Join(parts, ",") + ">"
}

// keyText is a lossless encoding used for map keys (constructor names may
// themselves contain angle brackets or commas, so the canonical text is not
// injective).
func keyText(t Type) string {
	if t.isVar {
		return "!" + strconv.Itoa(t.vari)
	}
	var b strings.Builder
	b.WriteString(strconv.Quote(t.name))
	if len(t.args) > 0 {
		b.WriteByte('(')
		for i, a := range t.args {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(keyText(a))
		}
		b.WriteByte(')')
	}
	return b.String()
}

func collectVars(t Type, out map[int]bool) {
	if t.isVar {
		out[t.vari] = true
		return
	}
	for _, a := range t.args {
		collectVars(a, out)
	}
}

// equalType is structural equality; variables are rigid and equal only the
// same variable index.
func equalType(a, b Type) bool {
	if a.isVar != b.isVar {
		return false
	}
	if a.isVar {
		return a.vari == b.vari
	}
	if a.name != b.name || len(a.args) != len(b.args) {
		return false
	}
	for i := range a.args {
		if !equalType(a.args[i], b.args[i]) {
			return false
		}
	}
	return true
}

// alphaEqual reports whether a and b are identical after renumbering each
// side's variables by preorder first occurrence.
func alphaEqual(a, b Type) bool {
	return alphaRec(a, b, map[int]int{}, map[int]int{})
}

func alphaRec(a, b Type, ab, ba map[int]int) bool {
	if a.isVar != b.isVar {
		return false
	}
	if a.isVar {
		if mapped, ok := ab[a.vari]; ok {
			return mapped == b.vari
		}
		if _, used := ba[b.vari]; used {
			return false
		}
		ab[a.vari] = b.vari
		ba[b.vari] = a.vari
		return true
	}
	if a.name != b.name || len(a.args) != len(b.args) {
		return false
	}
	for i := range a.args {
		if !alphaRec(a.args[i], b.args[i], ab, ba) {
			return false
		}
	}
	return true
}

// matchPattern reports whether pattern matches target, recording bindings of
// pattern variables in subst. Variables occurring in target are treated as
// pairwise distinct, non-substitutable constants. A pattern variable
// occurring several times must be bound to the same type everywhere
// (non-linear patterns).
func matchPattern(pattern, target Type, subst map[int]Type) bool {
	if pattern.isVar {
		if bound, ok := subst[pattern.vari]; ok {
			return equalType(bound, target)
		}
		subst[pattern.vari] = target
		return true
	}
	if target.isVar || pattern.name != target.name || len(pattern.args) != len(target.args) {
		return false
	}
	for i := range pattern.args {
		if !matchPattern(pattern.args[i], target.args[i], subst) {
			return false
		}
	}
	return true
}

// applySubst replaces every variable of t by its binding. All variables of t
// are expected to be bound (context variables are a subset of head
// variables, and matching binds every head variable).
func applySubst(t Type, subst map[int]Type) Type {
	if t.isVar {
		if bound, ok := subst[t.vari]; ok {
			return bound
		}
		return t
	}
	args := make([]Type, len(t.args))
	for i, a := range t.args {
		args[i] = applySubst(a, subst)
	}
	return Type{name: t.name, args: args, ok: true}
}
