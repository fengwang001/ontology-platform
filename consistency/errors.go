package consistency

type invalidArgumentError struct{}

func (invalidArgumentError) Error() string { return "invalid argument" }

type timestampNotAdvancingError struct{}

func (timestampNotAdvancingError) Error() string { return "timestamp not advancing" }

type notReadyError struct{}

func (notReadyError) Error() string { return "not ready: no consistent time point yet" }

type tooOldError struct{}

func (tooOldError) Error() string { return "time point too old: evicted from every view history" }
