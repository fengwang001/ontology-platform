package viewgraph

import "errors"

var (
	ErrEmptyName          = errors.New("viewgraph: empty view name")
	ErrDuplicateName      = errors.New("viewgraph: duplicate view name")
	ErrDependencyNotFound = errors.New("viewgraph: dependency is not registered")
	ErrCycle              = errors.New("viewgraph: dependency cycle detected")
	ErrNilCompute         = errors.New("viewgraph: nil compute function")
	ErrViewNotFound       = errors.New("viewgraph: view is not registered")
	ErrNotBaseView        = errors.New("viewgraph: view is not a base view")
	ErrViewNotSet         = errors.New("viewgraph: view value is not set")
	ErrDependencyNotSet   = errors.New("viewgraph: a dependency value is not set")
	ErrDependencyDirty    = errors.New("viewgraph: a dependency is still dirty")
)
