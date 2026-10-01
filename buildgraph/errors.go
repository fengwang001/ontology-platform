package buildgraph

import "fmt"

// Sentinel errors. Compare with errors.Is.
var (
	ErrEmptyID           = fmt.Errorf("buildgraph: edge id is empty")
	ErrDuplicateID       = fmt.Errorf("buildgraph: edge id already exists")
	ErrNoOutputs         = fmt.Errorf("buildgraph: edge has no outputs")
	ErrEmptyPath         = fmt.Errorf("buildgraph: path is empty")
	ErrOutputOwned       = fmt.Errorf("buildgraph: output is already produced by another edge")
	ErrPathIsInAndOut    = fmt.Errorf("buildgraph: path appears both as output and as input")
	ErrCycle             = fmt.Errorf("buildgraph: adding edge would create a dependency cycle")
	ErrEdgeNotFound      = fmt.Errorf("buildgraph: edge does not exist")
	ErrOutputSetMismatch = fmt.Errorf("buildgraph: complete outputs do not match the edge outputs")
	ErrInvalidMtime      = fmt.Errorf("buildgraph: mtime must be >= 1")
	ErrMissingInput      = fmt.Errorf("buildgraph: required input file does not exist")
	ErrTargetNotInGraph  = fmt.Errorf("buildgraph: target path is neither an input nor an output of any edge")
)

// MissingSourceError describes a source file missing from the dependency
// closure of a DirtySet call. The error is reported after every edge in the
// closure has been checked in edge-id byte order, and inside one edge in the
// order explicit inputs, implicit inputs, order-only inputs (index order).
type MissingSourceError struct {
	EdgeID string
	Path   string
}

func (e *MissingSourceError) Error() string {
	return fmt.Sprintf("buildgraph: missing source input %q required by edge %q", e.Path, e.EdgeID)
}

func (e *MissingSourceError) Is(target error) bool { return target == ErrMissingInput }

// TargetError carries the first target path (in argument order) that is not
// part of the graph.
type TargetError struct {
	Path string
}

func (e *TargetError) Error() string {
	return fmt.Sprintf("buildgraph: target %q is not in the graph", e.Path)
}

func (e *TargetError) Is(target error) bool { return target == ErrTargetNotInGraph }
