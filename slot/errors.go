package slot

type slotError string

func (e slotError) Error() string { return string(e) }

func newError(msg string) error { return slotError(msg) }
