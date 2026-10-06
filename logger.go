package matching

import (
	"fmt"
	"log"
)

type Logger interface {
	Printf(format string, args ...any)
}

type StdLogger struct {
	Logger *log.Logger
}

func (l StdLogger) Printf(format string, args ...any) {
	if l.Logger != nil {
		l.Logger.Printf(format, args...)
	}
}

type CaptureLogger struct {
	Lines []string
}

func (l *CaptureLogger) Printf(format string, args ...any) {
	l.Lines = append(l.Lines, fmt.Sprintf(format, args...))
}
