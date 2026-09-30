package ratelimit

import (
	"log"
	"strings"
	"time"
)

// Logger receives one structured line per attempt. Implementations must be
// safe for concurrent use.
type Logger interface {
	LogAttempt(account, source string, passwordCorrect bool, now time.Time, res Result, basis string)
}

// defaultLogger writes every attempt to stderr using the standard logger.
type defaultLogger struct{}

func (defaultLogger) LogAttempt(account, source string, passwordCorrect bool, now time.Time, res Result, basis string) {
	dims := make([]string, len(res.Dimensions))
	for i, d := range res.Dimensions {
		dims[i] = string(d)
	}
	log.Printf("login-limiter input={account=%q source=%q password_correct=%t now=%s} "+
		"output={allowed=%t locked=%v rejected=%v reason=%q unlock_at=%s dimensions=%s} basis=%s",
		account, source, passwordCorrect, now.Format(time.RFC3339Nano),
		res.Allowed, res.Locked, res.Rejected, res.Reason,
		formatUnlockAt(res.UnlockAt), strings.Join(dims, ","), basis)
}

func formatUnlockAt(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339Nano)
}

// SetLogger replaces the attempt logger. Passing nil restores the default
// stderr logger.
func (l *Limiter) SetLogger(logger Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if logger == nil {
		l.logger = defaultLogger{}
		return
	}
	l.logger = logger
}

func (l *Limiter) logAttempt(account, source string, passwordCorrect bool, now time.Time, res Result, basis string) {
	l.mu.Lock()
	logger := l.logger
	l.mu.Unlock()
	logger.LogAttempt(account, source, passwordCorrect, now, res, basis)
}
