package tablespace

import "errors"

// ErrInvalidArgument is reported first when any constructor parameter, page
// number, or hint value is out of range.
var ErrInvalidArgument = errors.New("tablespace: invalid argument")

// ErrSegmentNotFound is reported when the segment does not exist (never
// created or already freed).
var ErrSegmentNotFound = errors.New("tablespace: segment not found")

// ErrPageNotOwned is reported by FreePage when the page is free or owned by
// another segment.
var ErrPageNotOwned = errors.New("tablespace: page not owned by segment")

// ErrNoSpace is reported when AllocPage cannot place a page without changing
// any state.
var ErrNoSpace = errors.New("tablespace: no space available")
