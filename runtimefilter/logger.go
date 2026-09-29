package runtimefilter

// Logger 记录每次判定的输入、输出与依据。默认不输出，调用方可注入。
type Logger interface {
	Logf(format string, args ...any)
}
