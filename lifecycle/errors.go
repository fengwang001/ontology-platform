package lifecycle

type errInvalidConfig struct{}
type errInvalidArgs struct{}
type errClockRollback struct{}
type errNotFound struct{}

func (errInvalidConfig) Error() string { return "lifecycle: invalid configuration" }
func (errInvalidArgs) Error() string   { return "lifecycle: invalid arguments" }
func (errClockRollback) Error() string { return "lifecycle: clock moved backwards" }
func (errNotFound) Error() string      { return "lifecycle: object not found" }
