package window

// Window is a fixed-capacity ring buffer of the most recent bytes.
type Window struct {
	cap int
}

// New returns a window; cap must be positive.
func New(capacity int) *Window { return &Window{cap: capacity} }
