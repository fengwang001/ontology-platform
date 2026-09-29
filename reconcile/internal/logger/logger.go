// Package logger holds the structured logger used by the reconcile package,
// so tests can redirect and inspect log output.
package logger

import (
	"io"
	"log/slog"
	"sync"
)

var (
	mu sync.RWMutex
	// The default logger discards output so tests and embedders stay quiet;
	// call Set to observe inputs, diff keys and decisions.
	l = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
)

// Get returns the current structured logger.
func Get() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return l
}

// Set replaces the logger and returns a restore function.
func Set(logger *slog.Logger) func() {
	mu.Lock()
	defer mu.Unlock()
	prev := l
	l = logger
	return func() {
		mu.Lock()
		l = prev
		mu.Unlock()
	}
}

// NewTextHandler builds a text handler writing to w at the given level.
func NewTextHandler(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
