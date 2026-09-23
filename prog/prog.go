package prog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrInvalidProgram = errors.New("invalid program")
	ErrEmptyProgram   = errors.New("empty program")
)

type LineError struct {
	Line int
	Err  error
}

func (e LineError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e LineError) Unwrap() error { return e.Err }

type Op int

const (
	Lock Op = iota
	LockTimeout
	Unlock
	Work
	Done
)

type Instr struct {
	Op    Op
	Mutex string
	Steps int
	Line  int
}

type Program []Instr

func Parse(src string) (Program, error) {
	lines := strings.Split(src, "\n")
	p := Program{}
	declared := map[string]bool{}
	for i, raw := range lines {
		line := i + 1
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		ins := Instr{Line: line}
		switch fields[0] {
		case "Lock":
			if len(fields) != 2 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			ins.Op, ins.Mutex = Lock, fields[1]
		case "LockTimeout":
			if len(fields) != 3 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			n, err := strconv.Atoi(fields[2])
			if err != nil || n <= 0 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			ins.Op, ins.Mutex, ins.Steps = LockTimeout, fields[1], n
		case "Unlock":
			if len(fields) != 2 || !declared[fields[1]] {
				return nil, LineError{line, ErrInvalidProgram}
			}
			ins.Op, ins.Mutex = Unlock, fields[1]
		case "Work":
			if len(fields) != 2 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil || n <= 0 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			ins.Op, ins.Steps = Work, n
		case "Done":
			if len(fields) != 1 {
				return nil, LineError{line, ErrInvalidProgram}
			}
			ins.Op = Done
		default:
			return nil, LineError{line, ErrInvalidProgram}
		}
		if ins.Op == Lock || ins.Op == LockTimeout {
			declared[ins.Mutex] = true
		}
		p = append(p, ins)
	}
	if len(p) == 0 || p[len(p)-1].Op != Done {
		return nil, ErrEmptyProgram
	}
	return p, nil
}

func (p Program) Validate() error {
	locked := map[string]bool{}
	for _, in := range p {
		switch in.Op {
		case Lock, LockTimeout:
			if in.Mutex == "" || in.Op == LockTimeout && in.Steps <= 0 {
				return LineError{in.Line, ErrInvalidProgram}
			}
		case Unlock:
			if !locked[in.Mutex] {
				return LineError{in.Line, ErrInvalidProgram}
			}
			delete(locked, in.Mutex)
		case Work:
			if in.Steps <= 0 {
				return LineError{in.Line, ErrInvalidProgram}
			}
		case Done:
		}
		if in.Op == Lock || in.Op == LockTimeout {
			locked[in.Mutex] = true
		}
	}
	return nil
}
