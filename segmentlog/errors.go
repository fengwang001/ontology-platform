package segmentlog

// logError 是带固定身份的错误值，便于 errors.Is 精确区分错误类别。
type logError string

func newError(msg string) error { return logError(msg) }

func (e logError) Error() string { return string(e) }
