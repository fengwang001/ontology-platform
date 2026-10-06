package initsession

// kind identifies what a registered name denotes.
type kind int

const (
	kindPredeclared kind = iota + 1
	kindVariable
	kindFunction
)

// nameInfo records the declaration that owns a name.
type nameInfo struct {
	kind      kind
	unitIndex int // owning unit index for variables; -1 otherwise
	funcIndex int // function index for functions; -1 otherwise
}

// unit is one accepted variable initialization unit.
type unit struct {
	index int
	// vars are the left-hand side identifiers in source order; blank
	// identifiers ("_") are kept for positioning but never scheduled as
	// named variables.
	vars []string
	// refs are the deduplicated right-hand side references in
	// registration (arbitrary) order; they are validated at solve time.
	refs []string
	// blankVars counts blank identifiers on the left-hand side; they
	// create no namespace entry and cannot be referenced.
	blankVars int
}

// fn is one accepted function declaration.
type fn struct {
	index int
	name  string
	// refs are deduplicated identifiers directly mentioned in the body.
	refs []string
}

// declKind tags an entry in the global registration order log.
type declKind int

const (
	declUnit declKind = iota + 1
	declFunction
)

// decl records one accepted registration in global source order. The
// index points into snapshot.units or snapshot.functions depending on
// kind.
type decl struct {
	kind  declKind
	index int
}

// UnitDeps reports one unit's transitive variable dependencies.
type UnitDeps struct {
	// UnitIndex is the zero based registration order of the unit.
	UnitIndex int
	// Variables are the left-hand side variable names in source order,
	// blanks included.
	Variables []string
	// Deps are the transitive variable dependencies by ascending name,
	// excluding the unit's own variables and blank identifiers.
	Deps []string
}

// Result is the output of Solve.
type Result struct {
	// Order gives unit indices in initialization order.
	Order []int
	// Dependencies gives, for every registered unit (in registration
	// order), its transitive variable dependency set.
	Dependencies []UnitDeps
}
