// Package apply executes rename steps under the namespace lock and
// writes an undo log; it also parses possibly truncated logs.
package apply

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"
	"strings"

	"ontology/name"
	"ontology/plan"
)

var (
	ErrHeaderIncomplete = errors.New("apply: log header incomplete")
	ErrRecordIncomplete = errors.New("apply: log record incomplete")
	ErrCRCMismatch      = errors.New("apply: log body CRC mismatch")
	ErrStepFailed       = errors.New("apply: step failed, rolled back")
)

// EncodeLog renders the log: a header line with record count and the
// CRC32 of the whole body, then one "hex(from) hex(to)" line per step.
func EncodeLog(steps []plan.Step) []byte {
	var body []byte
	for _, s := range steps {
		body = append(body, hex.EncodeToString([]byte(s.From))...)
		body = append(body, ' ')
		body = append(body, hex.EncodeToString([]byte(s.To))...)
		body = append(body, '\n')
	}
	head := fmt.Sprintf("ONT1 %08x %08x\n", len(steps), crc32.ChecksumIEEE(body))
	return append([]byte(head), body...)
}

// ParseLog parses a possibly truncated log. It returns the maximal
// recoverable prefix of steps, the total step count from the header
// (-1 if the header is unreadable) and a classifiable error:
// ErrHeaderIncomplete, ErrRecordIncomplete or ErrCRCMismatch.
func ParseLog(data []byte) (steps []plan.Step, total int, err error) {
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil, -1, ErrHeaderIncomplete
	}
	fields := strings.Fields(string(data[:nl]))
	if len(fields) != 3 || fields[0] != "ONT1" {
		return nil, -1, ErrHeaderIncomplete
	}
	count, e1 := strconv.ParseUint(fields[1], 16, 32)
	crc, e2 := strconv.ParseUint(fields[2], 16, 32)
	if e1 != nil || e2 != nil {
		return nil, -1, ErrHeaderIncomplete
	}
	total = int(count)
	body := data[nl+1:]
	for len(body) > 0 {
		i := bytes.IndexByte(body, '\n')
		if i < 0 {
			return steps, total, ErrRecordIncomplete
		}
		parts := strings.Split(string(body[:i]), " ")
		if len(parts) != 2 {
			return steps, total, ErrRecordIncomplete
		}
		from, e1 := hex.DecodeString(parts[0])
		to, e2 := hex.DecodeString(parts[1])
		if e1 != nil || e2 != nil {
			return steps, total, ErrRecordIncomplete
		}
		steps = append(steps, plan.Step{From: string(from), To: string(to)})
		body = body[i+1:]
	}
	if crc32.ChecksumIEEE(data[nl+1:]) != uint32(crc) || len(steps) != total {
		return steps, total, ErrCRCMismatch
	}
	return steps, total, nil
}

// Executor runs steps against a namespace while holding its lock.
type Executor struct {
	NS *name.Set
	// Hook runs before each step; a non-nil result aborts the batch.
	Hook func(step int, s plan.Step) error
}

// Run writes the undo log, then executes steps under the namespace lock.
// On any failure it rolls back the already executed steps in reverse
// order, removes the log and returns an error wrapping ErrStepFailed.
func (e *Executor) Run(steps []plan.Step, logPath string) error {
	if err := os.WriteFile(logPath, EncodeLog(steps), 0o600); err != nil {
		return err
	}
	e.NS.Lock()
	defer e.NS.Unlock()
	for i, s := range steps {
		if e.Hook != nil {
			if err := e.Hook(i, s); err != nil {
				e.rollback(steps[:i])
				os.Remove(logPath)
				return fmt.Errorf("%w: step %d: %v", ErrStepFailed, i, err)
			}
		}
		if err := e.NS.RenameLocked(s.From, s.To); err != nil {
			e.rollback(steps[:i])
			os.Remove(logPath)
			return fmt.Errorf("%w: step %d: %v", ErrStepFailed, i, err)
		}
	}
	return nil
}

func (e *Executor) rollback(done []plan.Step) {
	for i := len(done) - 1; i >= 0; i-- {
		_ = e.NS.RenameLocked(done[i].To, done[i].From)
	}
}
