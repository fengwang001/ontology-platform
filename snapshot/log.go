package snapshot

import (
	"fmt"
	"io"
)

// decisionLogger records each step's inputs, retained snapshots, deleted files
// and the rationale behind each decision.
type decisionLogger struct {
	w io.Writer
}

func newDecisionLogger(w io.Writer) *decisionLogger {
	return &decisionLogger{w: w}
}

func (l *decisionLogger) printf(format string, args ...any) {
	if l == nil {
		return
	}
	fmt.Fprintf(l.w, format+"\n", args...)
}
