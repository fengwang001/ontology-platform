package slidingwindow

import "io"
import "log/slog"

// Option customizes a Window at construction.
type Option func(*Window)

// WithLogger routes the window's diagnostic logs to the given writer as
// JSON records. Passing nil keeps the package default logger.
func WithLogger(w io.Writer) Option {
	return func(win *Window) {
		if w == nil {
			win.logger = slog.Default()
			return
		}
		win.logger = slog.New(slog.NewJSONHandler(w, nil))
	}
}
