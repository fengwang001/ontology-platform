package objectcrypto

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
)

// TextLogger writes DecisionEvents as structured-ish text lines including the
// object id, ciphertext and the reason behind each allow/deny decision.
type TextLogger struct {
	logger *log.Logger
}

func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{logger: log.New(w, "objectcrypto ", log.LstdFlags|log.Lmicroseconds)}
}

func (l *TextLogger) LogDecision(_ context.Context, e DecisionEvent) {
	verdict := "DENY"
	if e.Allowed {
		verdict = "ALLOW"
	}
	l.logger.Printf("decision=%s op=%s object=%q field=%q key_version=%s ciphertext=%q reason=%q",
		verdict, e.Operation, e.ObjectID, e.Field, versionOfForLog(e.Ciphertext), string(e.Ciphertext), e.Reason)
}

func versionOfForLog(blob []byte) string {
	if len(blob) == 0 {
		return "-"
	}
	v, err := KeyVersionOf(blob)
	if err != nil {
		return "?"
	}
	return fmt.Sprintf("v%d", v)
}
