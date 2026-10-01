package exception

import (
	"errors"
	"sort"
	"sync"
)

type Entry struct {
	Start   int
	End     int
	Handler int
	Type    int
}

// Frame is one function activation on the stack.
type Frame struct {
	Function string
	PC       int
}

// Function is a registered name and its declaration-ordered exception table.
type Function struct {
	Name    string
	Entries []Entry
}

// ThrowResult identifies the handling frame and its resume location.
type ThrowResult struct {
	FrameIndex int
	HandlerPC  int
}

// ErrorCode is the stable, machine-readable discriminator for a rejected operation.
type ErrorCode string

const (
	// FunctionAlreadyExists means Register received a duplicate function name.
	FunctionAlreadyExists ErrorCode = "function_already_exists"
	// InvalidRange means an entry does not satisfy Start < End.
	InvalidRange ErrorCode = "invalid_range"
	// HandlerInsideRange means Handler belongs to the entry's own [Start, End).
	HandlerInsideRange ErrorCode = "handler_inside_range"
	// NegativeEntryType means an exception-table type is below zero.
	NegativeEntryType ErrorCode = "negative_entry_type"
	// FunctionNotFound means Push referenced an unregistered function.
	FunctionNotFound ErrorCode = "function_not_found"
	// NegativePC means Push received a negative program counter.
	NegativePC ErrorCode = "negative_pc"
	// ZeroReturnPC means a non-bottom frame used zero as its return address.
	ZeroReturnPC ErrorCode = "zero_return_pc"
	// InvalidThrownType means Throw received a non-positive exception type.
	InvalidThrownType ErrorCode = "invalid_thrown_type"
	// EmptyStack means Throw was called with no frames.
	EmptyStack ErrorCode = "empty_stack"
	// UncaughtException means no table entry matched any frame.
	UncaughtException ErrorCode = "uncaught_exception"
)

// Error carries a stable error code and contextual fields for a rejected call.
type Error struct {
	Operation string
	Code      ErrorCode
	Reason    string
	Function  string
	Entry     int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Reason
}

func (e *Error) Is(target error) bool {
	var other *Error
	if errors.As(target, &other) {
		return e.Code == other.Code
	}
	return false
}

var (
	// ErrFunctionAlreadyExists is the sentinel for duplicate registration.
	ErrFunctionAlreadyExists = &Error{Code: FunctionAlreadyExists, Reason: "function already exists"}
	// ErrInvalidRange is the sentinel for Start >= End.
	ErrInvalidRange = &Error{Code: InvalidRange, Reason: "entry start must be less than end"}
	// ErrHandlerInsideRange is the sentinel for a self-protecting handler.
	ErrHandlerInsideRange = &Error{Code: HandlerInsideRange, Reason: "handler must not be inside its own half-open range"}
	// ErrNegativeEntryType is the sentinel for negative table types.
	ErrNegativeEntryType = &Error{Code: NegativeEntryType, Reason: "entry type must not be negative"}
	// ErrFunctionNotFound is the sentinel for unknown Push functions.
	ErrFunctionNotFound = &Error{Code: FunctionNotFound, Reason: "function is not registered"}
	// ErrNegativePC is the sentinel for negative PCs.
	ErrNegativePC = &Error{Code: NegativePC, Reason: "pc must not be negative"}
	// ErrZeroReturnPC is the sentinel for zero on a non-bottom frame.
	ErrZeroReturnPC = &Error{Code: ZeroReturnPC, Reason: "return-address pc of a non-bottom frame must not be zero"}
	// ErrInvalidThrownType is the sentinel for non-positive thrown types.
	ErrInvalidThrownType = &Error{Code: InvalidThrownType, Reason: "thrown type must be a positive integer"}
	// ErrEmptyStack is the sentinel for throwing from an empty stack.
	ErrEmptyStack = &Error{Code: EmptyStack, Reason: "frame stack is empty"}
	// ErrUncaughtException is the sentinel when lookup reaches the bottom without a match.
	ErrUncaughtException = &Error{Code: UncaughtException, Reason: "no exception table entry matches"}
)

// Unwinder owns registered exception tables and a mutable call-frame stack.
type Unwinder struct {
	mu        sync.RWMutex
	functions map[string][]Entry
	frames    []Frame
}

// New creates an empty, concurrency-safe unwinder.
func New() *Unwinder {
	return &Unwinder{functions: make(map[string][]Entry)}
}

func registrationError(base *Error, name string, entryIndex int) error {
	return &Error{
		Operation: "register",
		Code:      base.Code,
		Reason:    base.Reason,
		Function:  name,
		Entry:     entryIndex,
	}
}

// Register validates and stores one function's declaration-ordered exception table.
func (u *Unwinder) Register(name string, entries []Entry) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, exists := u.functions[name]; exists {
		return registrationError(ErrFunctionAlreadyExists, name, -1)
	}

	for index, entry := range entries {
		if entry.Start >= entry.End {
			return registrationError(ErrInvalidRange, name, index)
		}
		if entry.Handler >= entry.Start && entry.Handler < entry.End {
			return registrationError(ErrHandlerInsideRange, name, index)
		}
		if entry.Type < 0 {
			return registrationError(ErrNegativeEntryType, name, index)
		}
	}

	table := make([]Entry, len(entries))
	copy(table, entries)
	u.functions[name] = table
	return nil
}

// Push appends a frame after validating its function and program counter.
func (u *Unwinder) Push(frame Frame) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, exists := u.functions[frame.Function]; !exists {
		return &Error{
			Operation: "push",
			Code:      FunctionNotFound,
			Reason:    ErrFunctionNotFound.Reason,
			Function:  frame.Function,
			Entry:     -1,
		}
	}
	if frame.PC < 0 {
		return &Error{
			Operation: "push",
			Code:      NegativePC,
			Reason:    ErrNegativePC.Reason,
			Function:  frame.Function,
			Entry:     -1,
		}
	}
	if len(u.frames) > 0 && frame.PC == 0 {
		return &Error{
			Operation: "push",
			Code:      ZeroReturnPC,
			Reason:    ErrZeroReturnPC.Reason,
			Function:  frame.Function,
			Entry:     -1,
		}
	}

	u.frames = append(u.frames, frame)
	return nil
}

// Throw searches frames top-to-bottom and truncates the stack at the handler frame.
func (u *Unwinder) Throw(exceptionType int) (ThrowResult, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if exceptionType <= 0 {
		return ThrowResult{}, &Error{
			Operation: "throw",
			Code:      InvalidThrownType,
			Reason:    ErrInvalidThrownType.Reason,
			Entry:     -1,
		}
	}
	if len(u.frames) == 0 {
		return ThrowResult{}, &Error{
			Operation: "throw",
			Code:      EmptyStack,
			Reason:    ErrEmptyStack.Reason,
			Entry:     -1,
		}
	}

	for frameIndex := len(u.frames) - 1; frameIndex >= 0; frameIndex-- {
		frame := u.frames[frameIndex]
		lookupPC := frame.PC
		if frameIndex != len(u.frames)-1 {
			lookupPC--
		}

		for _, entry := range u.functions[frame.Function] {
			if lookupPC >= entry.Start && lookupPC < entry.End && (entry.Type == 0 || entry.Type == exceptionType) {
				u.frames = append([]Frame(nil), u.frames[:frameIndex+1]...)
				u.frames[frameIndex].PC = entry.Handler
				return ThrowResult{FrameIndex: frameIndex, HandlerPC: entry.Handler}, nil
			}
		}
	}

	return ThrowResult{}, &Error{
		Operation: "throw",
		Code:      UncaughtException,
		Reason:    ErrUncaughtException.Reason,
		Entry:     -1,
	}
}

// Frames returns a copy of the current stack from bottom to top.
func (u *Unwinder) Frames() []Frame {
	u.mu.RLock()
	defer u.mu.RUnlock()

	frames := make([]Frame, len(u.frames))
	copy(frames, u.frames)
	return frames
}

// Functions returns registered functions sorted by name with copied table entries.
func (u *Unwinder) Functions() []Function {
	u.mu.RLock()
	defer u.mu.RUnlock()

	names := make([]string, 0, len(u.functions))
	for name := range u.functions {
		names = append(names, name)
	}
	sort.Strings(names)

	functions := make([]Function, 0, len(names))
	for _, name := range names {
		entries := make([]Entry, len(u.functions[name]))
		copy(entries, u.functions[name])
		functions = append(functions, Function{Name: name, Entries: entries})
	}
	return functions
}
