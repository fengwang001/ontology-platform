package reclaim

import "errors"

// ErrNotHeld is returned when releasing an ID that is out of range or not
// currently held.
var ErrNotHeld = errors.New("reclaim: id not held")
