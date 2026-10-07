// Package shallow implements boundary management and object reachability
// for a shallow (prefix-truncated) clone of a remote commit graph.
package shallow

import "errors"

// Commit is a node in the remote commit graph.
type Commit struct {
	ID       string   // unique identifier
	Parents  []string // parent commit ids, may be empty (root)
	Time     int64    // creation timestamp, not necessarily monotonic
	Contents []string // ids of content objects directly referenced
}

// ContentObject is a blob referenced by commits.
type ContentObject struct {
	ID   string
	Size int64
}

// Remote is the source of truth for the full commit graph.
// Implementations must return an error wrapping ErrObjectNotFound when the
// requested object does not exist; any other error is treated as a fetch
// failure (ErrFetchFailed) by the Repo.
type Remote interface {
	FetchCommit(id string) (Commit, error)
	FetchContent(id string) (ContentObject, error)
}

// Error taxonomy, checked in this exact precedence order by every
// operation: invalid parameter, ref not found, remote object not found,
// fetch failure, invalid local state.
var (
	ErrInvalidParam   = errors.New("shallow: invalid parameter")
	ErrRefNotFound    = errors.New("shallow: ref not found")
	ErrObjectNotFound = errors.New("shallow: object not found in remote")
	ErrFetchFailed    = errors.New("shallow: fetch failed")
	ErrInvalidState   = errors.New("shallow: invalid local state")
)

// classifyRemote maps a Remote error onto the taxonomy: ErrObjectNotFound
// stays distinguishable from any other (fetch) failure.
func classifyRemote(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrObjectNotFound) {
		return err
	}
	return &wrappedError{kind: ErrFetchFailed, err: err}
}

type wrappedError struct {
	kind error
	err  error
}

func (w *wrappedError) Error() string { return w.kind.Error() + ": " + w.err.Error() }
func (w *wrappedError) Unwrap() error { return w.kind }
