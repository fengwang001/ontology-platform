package versioned

import (
	"bytes"
	"log"
)

// newLineLogger writes timestamp-free, newline-terminated log lines to buf.
func newLineLogger(buf *bytes.Buffer) Logger {
	return log.New(buf, "", 0)
}
