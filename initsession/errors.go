package initsession

import "fmt"

// ErrorKind classifies registration and solving failures.
type ErrorKind int

const (
	// KindInvalidArgument: illegal identifier or empty variable list.
	KindInvalidArgument ErrorKind = iota + 1
	// KindRedeclared: name conflicts with an accepted declaration.
	KindRedeclared
	// KindUndeclared: a reference names no declared variable/function
	// and is not predeclared.
	KindUndeclared
	// KindInitCycle: remaining units form an initialization cycle.
	KindInitCycle
)

// Error is the distinguishable error returned by Session operations.
type Error struct {
	Kind ErrorKind
	// Message is a human readable description.
	Message string
	// UnitIndex is the registration-order index of the offending unit
	// (zero based), or -1 when the error is not attached to a unit.
	UnitIndex int
	// Function is the name of the offending function declaration, or "".
	Function string
	// Name is the offending identifier, when singular.
	Name string
	// Names are all offending identifiers, sorted, when plural
	// (cycled variables).
	Names []string
}

func (e *Error) Error() string {
	return fmt.Sprintf("initsession: %s", e.Message)
}

func invalidf(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidArgument, UnitIndex: -1, Message: fmt.Sprintf(format, args...)}
}

func redeclaredf(name string, unitIndex int, function string) *Error {
	return &Error{Kind: KindRedeclared, UnitIndex: unitIndex, Function: function, Name: name,
		Message: fmt.Sprintf("identifier %q redeclared", name)}
}
