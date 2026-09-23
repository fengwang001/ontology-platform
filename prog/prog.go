// Package prog defines task scripts: a small instruction language and its
// parser/validator. It depends on no other package.
package prog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Op is one instruction kind.
type Op int

const (
	Lock Op = iota
	LockTimeout
	Unlock
	Work
	Done
)

// Instr is one parsed instruction.
type Instr struct {
	Op   Op
	Mu   string
	N    int
	Line int
}

// Program is a validated script.
type Program struct {
	Instrs []Instr
	Decl   map[string]bool // locks ever declared via Lock/LockTimeout
}

// ProgError carries a 1-based line number for a malformed script.
type ProgError struct {
	Line int
	Msg  string
}

func (e *ProgError) Error() string { return fmt.Sprintf("prog: line %d: %s", e.Line, e.Msg) }

var (
	ErrUnknown = errors.New("unknown instruction")
	ErrWork0   = errors.New("Work requires n >= 1")
	ErrTimeout = errors.New("LockTimeout requires k >= 1")
	ErrArgs    = errors.New("wrong argument count")
	ErrName    = errors.New("mutex name required")
	ErrUnlock  = errors.New("Unlock of an undeclared mutex")
	ErrEmpty   = errors.New("empty program")
)

// Parse parses and validates a script, one instruction per line. An Unlock is
// illegal if the mutex is never declared by a Lock/LockTimeout anywhere.
func Parse(src string) (*Program, error) {
	type raw struct {
		f    []string
		line int
	}
	var raws []raw
	decl := map[string]bool{}
	for i, s := range strings.Split(src, "\n") {
		f := strings.Fields(s)
		if len(f) == 0 {
			continue
		}
		raws = append(raws, raw{f, i + 1})
		if (f[0] == "Lock" || f[0] == "LockTimeout") && len(f) >= 2 {
			decl[f[1]] = true
		}
	}
	if len(raws) == 0 {
		return nil, &ProgError{0, ErrEmpty.Error()}
	}
	p := &Program{Decl: decl}
	for _, r := range raws {
		f, line := r.f, r.line
		in := Instr{Line: line}
		switch f[0] {
		case "Lock":
			if len(f) != 2 {
				return nil, &ProgError{line, ErrArgs.Error()}
			}
			in.Op, in.Mu = Lock, f[1]
		case "LockTimeout":
			if len(f) != 3 {
				return nil, &ProgError{line, ErrArgs.Error()}
			}
			k, err := strconv.Atoi(f[2])
			if err != nil || k <= 0 {
				return nil, &ProgError{line, ErrTimeout.Error()}
			}
			in.Op, in.Mu, in.N = LockTimeout, f[1], k
		case "Unlock":
			if len(f) != 2 {
				return nil, &ProgError{line, ErrArgs.Error()}
			}
			if !decl[f[1]] {
				return nil, &ProgError{line, ErrUnlock.Error()}
			}
			in.Op, in.Mu = Unlock, f[1]
		case "Work":
			if len(f) != 2 {
				return nil, &ProgError{line, ErrArgs.Error()}
			}
			n, err := strconv.Atoi(f[1])
			if err != nil || n <= 0 {
				return nil, &ProgError{line, ErrWork0.Error()}
			}
			in.Op, in.N = Work, n
		case "Done":
			if len(f) != 1 {
				return nil, &ProgError{line, ErrArgs.Error()}
			}
			in.Op = Done
		default:
			return nil, &ProgError{line, ErrUnknown.Error()}
		}
		p.Instrs = append(p.Instrs, in)
	}
	return p, nil
}
