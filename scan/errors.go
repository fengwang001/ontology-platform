package scan

import "errors"

// ErrBadCursor is returned when a resume token is malformed or does not
// belong to the segment layout being scanned.
var ErrBadCursor = errors.New("scan: invalid cursor")
