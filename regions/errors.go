package regions

import "errors"

var (
	ErrCoordOutOfRange = errors.New("regions: coordinate out of range")
	ErrTooFewVertices  = errors.New("regions: polygon must have at least 3 vertices")
	ErrOuterNotConvex  = errors.New("regions: outer ring is not strictly convex")
	ErrHoleNotConvex   = errors.New("regions: hole is not strictly convex")
	ErrHoleNotInside   = errors.New("regions: hole vertices must be strictly inside the outer ring")
	ErrIDExists        = errors.New("regions: region id already exists")
	ErrIDNotFound      = errors.New("regions: region id not found")
)
