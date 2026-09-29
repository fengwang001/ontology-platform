package blockstore

import (
	"fmt"
	"log"
	"os"
	"time"
)

// Logger 是块库接受的最小日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

type stdLogger struct {
	l *log.Logger
}

func (l stdLogger) Printf(format string, args ...any) {
	l.l.Output(2, fmt.Sprintf(format, args...))
}

func newDefaultLogger() Logger {
	return stdLogger{l: log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)}
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// eventLogger 在每条日志前加上时间戳与操作名，统一打印 输入 / 输出 / 判定依据。
type eventLogger struct {
	next Logger
}

func (e eventLogger) logf(op, format string, args ...any) {
	ts := time.Now().Format("15:04:05.000000")
	e.next.Printf("%s op=%s %s", ts, op, fmt.Sprintf(format, args...))
}

func (e eventLogger) Printf(format string, args ...any) {
	e.logf("Generic", format, args...)
}
