package blockstore

import (
	"io"
	"log/slog"
)

// newLogger builds a text logger that records every operation's inputs,
// outputs and the reason for each decision, as required for auditing.
func newLogger(w io.Writer) *slog.Logger {
	if w == nil {
		w = io.Discard
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
