package chunkcache

import "fmt"

// Logger 为逐步判定日志接口。测试默认打印每步输入、输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// nopLogger 丢弃日志。
type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

// printLogger 输出到标准输出。
type printLogger struct{}

func (printLogger) Logf(format string, args ...any) { fmt.Printf(format+"\n", args...) }
