package migration

import "log"

// Logger 记录每一步迁移的输入、返回与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

type defaultLogger struct{}

func (defaultLogger) Printf(format string, args ...any) {
	log.Printf(format, args...)
}
