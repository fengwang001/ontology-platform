// Package register implements a linearizability checker for a single integer register.
package register

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrClientBusy        = errors.New("client already has an unfinished operation")
	ErrInvalidStartTime  = errors.New("invoke time is earlier than the client's previous completion time")
	ErrHistoryLimit      = errors.New("history cannot contain more than 20 operations")
	ErrUnknownOperation  = errors.New("operation id does not exist")
	ErrAlreadyCompleted  = errors.New("operation is already completed")
	ErrInvalidReturnTime = errors.New("return time is earlier than invoke time")
	ErrResultMismatch    = errors.New("result type does not match operation")
	ErrInvalidOperation  = errors.New("invalid operation kind")
)

type OpKind int

const (
	KindWrite OpKind = iota + 1
	KindRead
	KindCAS
)

type Op struct {
	Kind     OpKind
	Value    int
	Expected int
	New      int
}

type Operation struct {
	ID           int
	Client       string
	Kind         OpKind
	InvokeTime   int
	ReturnTime   int
	Completed    bool
	Value        int
	Expected     int
	New          int
	ReadValue    int
	CASSucceeded bool
}

type CheckResult struct {
	Linearizable bool
	Witness      []int
	Reason       string
}

type EndResult struct {
	ReadValue    int
	CASSucceeded bool
}

type Logger interface {
	Printf(format string, args ...any)
}

type Checker struct {
	Logger Logger

	mu      sync.RWMutex
	ops     []Operation
	nextID  int
	clients map[string]clientState
}

type clientState struct {
	pendingID int
	lastEnd   int
	hasLast   bool
}

func NewChecker() *Checker {
	return &Checker{
		nextID:  1,
		clients: make(map[string]clientState),
	}
}

func (c *Checker) Begin(client string, op Op, invokeTime int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if client == "" {
		return 0, errors.New("client must not be empty")
	}
	state := c.clients[client]
	if state.pendingID != 0 {
		return 0, ErrClientBusy
	}
	if state.hasLast && invokeTime < state.lastEnd {
		return 0, ErrInvalidStartTime
	}
	if len(c.ops) >= 20 {
		return 0, ErrHistoryLimit
	}
	if op.Kind != KindWrite && op.Kind != KindRead && op.Kind != KindCAS {
		return 0, ErrInvalidOperation
	}

	id := c.nextID
	c.nextID++
	c.ops = append(c.ops, Operation{
		ID:         id,
		Client:     client,
		Kind:       op.Kind,
		InvokeTime: invokeTime,
		Value:      op.Value,
		Expected:   op.Expected,
		New:        op.New,
	})
	state.pendingID = id
	c.clients[client] = state
	return id, nil
}

func (c *Checker) End(id, returnTime int, result *EndResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if id < 1 || id >= c.nextID {
		return ErrUnknownOperation
	}
	op := &c.ops[id-1]
	if op.Completed {
		return ErrAlreadyCompleted
	}
	if returnTime < op.InvokeTime {
		return ErrInvalidReturnTime
	}

	switch op.Kind {
	case KindWrite:
		if result != nil {
			return ErrResultMismatch
		}
	case KindRead, KindCAS:
		if result == nil {
			return ErrResultMismatch
		}
	}

	op.Completed = true
	op.ReturnTime = returnTime
	if op.Kind == KindRead {
		op.ReadValue = result.ReadValue
	}
	if op.Kind == KindCAS {
		op.CASSucceeded = result.CASSucceeded
	}

	state := c.clients[op.Client]
	state.pendingID = 0
	state.lastEnd = returnTime
	state.hasLast = true
	c.clients[op.Client] = state
	return nil
}

func (c *Checker) Snapshot() []Operation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := make([]Operation, len(c.ops))
	copy(snapshot, c.ops)
	return snapshot
}

func (c *Checker) Check() CheckResult {
	snapshot := c.Snapshot()
	linearizable, witness, reason := findWitness(snapshot)
	c.logf("check input=%s output={linearizable:%t witness:%v} reason=%q", formatHistory(snapshot), linearizable, witness, reason)
	return CheckResult{Linearizable: linearizable, Witness: witness, Reason: reason}
}

func (c *Checker) logf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Printf(format, args...)
	}
}

func operationName(kind OpKind) string {
	switch kind {
	case KindWrite:
		return "write"
	case KindRead:
		return "read"
	case KindCAS:
		return "cas"
	default:
		return fmt.Sprintf("unknown(%d)", kind)
	}
}

func formatHistory(ops []Operation) string {
	formatted := make([]string, 0, len(ops))
	for _, op := range ops {
		formatted = append(formatted, formatOp(op))
	}
	return "[" + strings.Join(formatted, ", ") + "]"
}

func formatOp(op Operation) string {
	if !op.Completed {
		switch op.Kind {
		case KindWrite:
			return fmt.Sprintf("#%d write(%d) client=%s invoke=%d pending", op.ID, op.Value, op.Client, op.InvokeTime)
		case KindRead:
			return fmt.Sprintf("#%d read() client=%s invoke=%d pending", op.ID, op.Client, op.InvokeTime)
		case KindCAS:
			return fmt.Sprintf("#%d cas(%d,%d) client=%s invoke=%d pending", op.ID, op.Expected, op.New, op.Client, op.InvokeTime)
		}
	}

	switch op.Kind {
	case KindWrite:
		return fmt.Sprintf("#%d write(%d) client=%s invoke=%d return=%d ok", op.ID, op.Value, op.Client, op.InvokeTime, op.ReturnTime)
	case KindRead:
		return fmt.Sprintf("#%d read()=%d client=%s invoke=%d return=%d", op.ID, op.ReadValue, op.Client, op.InvokeTime, op.ReturnTime)
	case KindCAS:
		return fmt.Sprintf("#%d cas(%d,%d)=%t client=%s invoke=%d return=%d", op.ID, op.Expected, op.New, op.CASSucceeded, op.Client, op.InvokeTime, op.ReturnTime)
	default:
		return fmt.Sprintf("#%d unknown client=%s invoke=%d return=%d", op.ID, op.Client, op.InvokeTime, op.ReturnTime)
	}
}
