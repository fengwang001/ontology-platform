package ontology

import "errors"

// The three mutually exclusive conflict classes, distinguishable by errors.Is
// as well as by Result.Kind.
var (
	ErrInstanceDeleted  = errors.New("instance deleted")
	ErrStaleBase        = errors.New("stale baseline")
	ErrPropertyConflict = errors.New("property set conflict")
)
