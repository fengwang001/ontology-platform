package flowcontrol

import "log"

// Logger 记录每次调用的输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Printf(string, ...any) {}

type stdLogger struct{}

func (stdLogger) Printf(format string, args ...any) {
	log.Printf(format, args...)
}
