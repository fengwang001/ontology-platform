package ontology

import (
	"fmt"
	"io"
	"strings"
)

// WriteLogger prints one line per decision: inputs, output and the basis that
// produced it. It satisfies the required "每次判定的输入、输出与依据" log.
type WriteLogger struct {
	W io.Writer
}

// LogDecision implements Logger.
func (l WriteLogger) LogDecision(d Decision) {
	basis := strings.Join(d.Basis, " | ")
	if basis == "" {
		basis = "-"
	}
	answer := "deny(" + string(d.DenyReason) + ")"
	if d.Allowed {
		answer = "allow"
	}
	fmt.Fprintf(l.W, "decide subject=%s object=%s action=%s => %s basis=[%s]\n",
		d.Subject, d.Object, d.Action, answer, basis)
}
