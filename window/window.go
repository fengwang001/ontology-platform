package window

// Window is a fixed-capacity ring of already produced bytes.
type Window struct {
	capacity int
}

func New(capacity int) *Window { return &Window{capacity: capacity} }
