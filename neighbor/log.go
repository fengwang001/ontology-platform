package neighbor

// Logger 记录每次操作的输入、输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

// DiscardLogger 不输出任何日志。
func DiscardLogger() Logger { return nopLogger{} }
