package merge3

import "errors"

// ErrEmptyLabel is returned by Result.Render when either label is empty.
var ErrEmptyLabel = errors.New("merge3: render labels must not be empty")
