package exception

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

type naiveState struct {
	functions map[string][]Entry
	frames    []Frame
}

func newNaiveState() *naiveState {
	return &naiveState{functions: make(map[string][]Entry)}
}

func (s *naiveState) register(name string, entries []Entry) error {
	if _, exists := s.functions[name]; exists {
		return ErrFunctionAlreadyExists
	}
	for index, entry := range entries {
		if entry.Start >= entry.End {
			return fmt.Errorf("%w at entry %d", ErrInvalidRange, index)
		}
		if entry.Handler >= entry.Start && entry.Handler < entry.End {
			return fmt.Errorf("%w at entry %d", ErrHandlerInsideRange, index)
		}
		if entry.Type < 0 {
			return fmt.Errorf("%w at entry %d", ErrNegativeEntryType, index)
		}
	}
	s.functions[name] = append([]Entry(nil), entries...)
	return nil
}

func (s *naiveState) push(frame Frame) error {
	if _, exists := s.functions[frame.Function]; !exists {
		return ErrFunctionNotFound
	}
	if frame.PC < 0 {
		return ErrNegativePC
	}
	if len(s.frames) > 0 && frame.PC == 0 {
		return ErrZeroReturnPC
	}
	s.frames = append(s.frames, frame)
	return nil
}

func (s *naiveState) throw(exceptionType int) (ThrowResult, []Frame, string, error) {
	if exceptionType <= 0 {
		return ThrowResult{}, append([]Frame(nil), s.frames...), "thrown type is not positive", ErrInvalidThrownType
	}
	if len(s.frames) == 0 {
		return ThrowResult{}, nil, "stack has no frame", ErrEmptyStack
	}

	for frameIndex := len(s.frames) - 1; frameIndex >= 0; frameIndex-- {
		frame := s.frames[frameIndex]
		lookupPC := frame.PC
		if frameIndex < len(s.frames)-1 {
			lookupPC--
		}
		for entryIndex, entry := range s.functions[frame.Function] {
			if lookupPC >= entry.Start && lookupPC < entry.End && (entry.Type == 0 || entry.Type == exceptionType) {
				s.frames = append([]Frame(nil), s.frames[:frameIndex+1]...)
				s.frames[frameIndex].PC = entry.Handler
				basis := fmt.Sprintf(
					"frame=%d function=%s raw_pc=%d lookup_pc=%d entry=%d range=[%d,%d) handler=%d entry_type=%d thrown_type=%d",
					frameIndex, frame.Function, frame.PC, lookupPC, entryIndex,
					entry.Start, entry.End, entry.Handler, entry.Type, exceptionType,
				)
				return ThrowResult{FrameIndex: frameIndex, HandlerPC: entry.Handler}, append([]Frame(nil), s.frames...), basis, nil
			}
		}
	}

	return ThrowResult{}, append([]Frame(nil), s.frames...), "all frames were checked without a match", ErrUncaughtException
}

func registerForTest(t *testing.T, u *Unwinder, name string, entries []Entry) {
	t.Helper()
	if err := u.Register(name, entries); err != nil {
		t.Fatalf("Register(%q, %+v): %v", name, entries, err)
	}
}

func pushForTest(t *testing.T, u *Unwinder, frame Frame) {
	t.Helper()
	if err := u.Push(frame); err != nil {
		t.Fatalf("Push(%+v): %v", frame, err)
	}
}

func assertErrorCode(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func logCase(t *testing.T, input string, frames []Frame, result ThrowResult, err error, basis string) {
	t.Helper()
	t.Logf("input=%s output={result=%+v error=%v frames=%+v} basis=%s", input, result, err, frames, basis)
}

func TestLookupBoundariesAndDeclarationOrder(t *testing.T) {
	type lookupCase struct {
		name        string
		entries     []Entry
		tables      map[string][]Entry
		frames      []Frame
		thrownType  int
		want        ThrowResult
		wantFrames  []Frame
		lookupInput string
		basis       string
	}

	tests := []lookupCase{
		{
			name: "return address at range end uses call pc",
			entries: []Entry{
				{Start: 10, End: 20, Handler: 100, Type: 7},
			},
			frames: []Frame{
				{Function: "caller", PC: 20},
				{Function: "callee", PC: 5},
			},
			thrownType:  7,
			want:        ThrowResult{FrameIndex: 0, HandlerPC: 100},
			wantFrames:  []Frame{{Function: "caller", PC: 100}},
			lookupInput: "caller pc=20 below top; callee pc=5 top; type=7",
			basis:       "caller lookup p=19 belongs to [10,20), although return-address pc=20 is excluded",
		},
		{
			name: "return address at range start misses",
			entries: []Entry{
				{Start: 20, End: 30, Handler: 100, Type: 7},
			},
			frames: []Frame{
				{Function: "caller", PC: 20},
				{Function: "callee", PC: 5},
			},
			thrownType:  7,
			lookupInput: "caller pc=20 below top; callee pc=5 top; type=7",
			basis:       "caller lookup p=19 is before Start=20, so [20,30) does not match",
		},
		{
			name: "top pc at range end misses",
			entries: []Entry{
				{Start: 10, End: 20, Handler: 100, Type: 7},
			},
			frames: []Frame{
				{Function: "only", PC: 20},
			},
			thrownType:  7,
			lookupInput: "top pc=20; type=7",
			basis:       "the top frame uses p=pc=20 directly and [10,20) excludes 20",
		},
		{
			name: "wildcard declared before specific type wins",
			entries: []Entry{
				{Start: 0, End: 10, Handler: 100, Type: 0},
				{Start: 0, End: 10, Handler: 200, Type: 7},
			},
			frames:      []Frame{{Function: "order", PC: 5}},
			thrownType:  7,
			want:        ThrowResult{FrameIndex: 0, HandlerPC: 100},
			wantFrames:  []Frame{{Function: "order", PC: 100}},
			lookupInput: "top pc=5; type=7",
			basis:       "wildcard entry index 0 and concrete entry index 1 both match, so index 0 wins",
		},
		{
			name: "specific type declared before wildcard wins",
			entries: []Entry{
				{Start: 0, End: 10, Handler: 200, Type: 7},
				{Start: 0, End: 10, Handler: 100, Type: 0},
			},
			frames:      []Frame{{Function: "order", PC: 5}},
			thrownType:  7,
			want:        ThrowResult{FrameIndex: 0, HandlerPC: 200},
			wantFrames:  []Frame{{Function: "order", PC: 200}},
			lookupInput: "top pc=5; type=7",
			basis:       "concrete entry index 0 and wildcard entry index 1 both match, so index 0 wins",
		},
		{
			name: "inner entry declared before outer entry catches first",
			entries: []Entry{
				{Start: 8, End: 9, Handler: 300, Type: 0},
				{Start: 0, End: 20, Handler: 100, Type: 0},
			},
			frames: []Frame{
				{Function: "outer", PC: 0},
				{Function: "inner", PC: 8},
			},
			thrownType: 1,
			want:       ThrowResult{FrameIndex: 1, HandlerPC: 300},
			wantFrames: []Frame{
				{Function: "outer", PC: 0},
				{Function: "inner", PC: 300},
			},
			lookupInput: "inner top pc=8; outer return pc=0; type=1",
			basis:       "frame search starts at the top and inner entry index 0 matches before outer is inspected",
		},
		{
			name: "outer entry catches and truncates inner frames",
			tables: map[string][]Entry{
				"outer":  {{Start: 0, End: 10, Handler: 400, Type: 3}},
				"middle": {{Start: 20, End: 30, Handler: 401, Type: 3}},
				"inner":  {{Start: 20, End: 30, Handler: 402, Type: 3}},
			},
			frames: []Frame{
				{Function: "outer", PC: 5},
				{Function: "middle", PC: 9},
				{Function: "inner", PC: 2},
			},
			thrownType:  3,
			want:        ThrowResult{FrameIndex: 0, HandlerPC: 400},
			wantFrames:  []Frame{{Function: "outer", PC: 400}},
			lookupInput: "inner top p=2; middle return lookup p=8; outer return lookup p=4; type=3",
			basis:       "inner and middle miss, outer matches and all frames above it are popped",
		},
		{
			name: "uncaught leaves every frame unchanged",
			tables: map[string][]Entry{
				"caller": {{Start: 30, End: 40, Handler: 300, Type: 2}},
				"callee": {{Start: 10, End: 20, Handler: 200, Type: 2}},
			},
			frames: []Frame{
				{Function: "caller", PC: 30},
				{Function: "callee", PC: 5},
			},
			thrownType:  2,
			lookupInput: "callee top p=5; caller return lookup p=29; type=2",
			basis:       "neither half-open range contains its lookup pc, so no frame is popped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := New()
			naive := newNaiveState()
			registered := make(map[string]bool)

			for _, frame := range tt.frames {
				if !registered[frame.Function] {
					table := tt.entries
					if tt.tables != nil {
						table = tt.tables[frame.Function]
					}
					registerForTest(t, u, frame.Function, table)
					if err := naive.register(frame.Function, table); err != nil {
						t.Fatalf("naive register: %v", err)
					}
					registered[frame.Function] = true
				}
			}
			for _, frame := range tt.frames {
				pushForTest(t, u, frame)
				if err := naive.push(frame); err != nil {
					t.Fatalf("naive push: %v", err)
				}
			}

			result, err := u.Throw(tt.thrownType)
			frames := u.Frames()
			naiveResult, naiveFrames, naiveBasis, naiveErr := naive.throw(tt.thrownType)
			logCase(t, tt.lookupInput, frames, result, err, tt.basis)
			t.Logf("naive output={result=%+v error=%v frames=%+v} basis=%s", naiveResult, naiveErr, naiveFrames, naiveBasis)

			if tt.wantFrames == nil {
				if err == nil {
					t.Fatalf("Throw() unexpectedly succeeded: %+v", result)
				}
				assertErrorCode(t, err, ErrUncaughtException)
				if !reflect.DeepEqual(frames, tt.frames) {
					t.Fatalf("frames after uncaught = %+v, want unchanged %+v", frames, tt.frames)
				}
			} else if err != nil || result != tt.want || !reflect.DeepEqual(frames, tt.wantFrames) {
				t.Fatalf("Throw() = %+v, %v, frames=%+v; want %+v, nil, frames=%+v", result, err, frames, tt.want, tt.wantFrames)
			}

			if !reflect.DeepEqual(result, naiveResult) || !reflect.DeepEqual(frames, naiveFrames) {
				t.Fatalf("implementation and naive simulation differ: got %+v/%+v, naive %+v/%+v", result, frames, naiveResult, naiveFrames)
			}
			if (err == nil) != (naiveErr == nil) {
				t.Fatalf("error status differs: implementation=%v naive=%v", err, naiveErr)
			}
		})
	}
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	u := New()
	registerForTest(t, u, "valid", []Entry{{Start: 1, End: 4, Handler: 8, Type: 2}})
	pushForTest(t, u, Frame{Function: "valid", PC: 1})

	registerCases := []struct {
		name    string
		fn      string
		entries []Entry
		want    error
	}{
		{"duplicate name before entry validation", "valid", []Entry{{Start: 9, End: 1, Handler: 5, Type: -1}}, ErrFunctionAlreadyExists},
		{"invalid range before handler and type", "bad-range", []Entry{{Start: 5, End: 5, Handler: 5, Type: -1}}, ErrInvalidRange},
		{"handler before negative type", "bad-handler", []Entry{{Start: 0, End: 5, Handler: 4, Type: -1}}, ErrHandlerInsideRange},
		{"negative type", "bad-type", []Entry{{Start: 0, End: 5, Handler: 6, Type: -1}}, ErrNegativeEntryType},
	}

	for _, tt := range registerCases {
		t.Run("register/"+tt.name, func(t *testing.T) {
			beforeFunctions := u.Functions()
			beforeFrames := u.Frames()
			err := u.Register(tt.fn, tt.entries)
			afterFunctions := u.Functions()
			afterFrames := u.Frames()
			assertErrorCode(t, err, tt.want)
			if !reflect.DeepEqual(beforeFunctions, afterFunctions) || !reflect.DeepEqual(beforeFrames, afterFrames) {
				t.Fatalf("rejected register changed state: before=%+v/%+v after=%+v/%+v", beforeFunctions, beforeFrames, afterFunctions, afterFrames)
			}
			t.Logf("input=Register name=%s entries=%+v output=%v basis=snapshots before and after rejection are equal", tt.fn, tt.entries, err)
		})
	}

	pushCases := []struct {
		name  string
		frame Frame
		want  error
	}{
		{"unknown function", Frame{Function: "missing", PC: 1}, ErrFunctionNotFound},
		{"negative pc", Frame{Function: "valid", PC: -1}, ErrNegativePC},
		{"zero return pc", Frame{Function: "valid", PC: 0}, ErrZeroReturnPC},
	}

	for _, tt := range pushCases {
		t.Run("push/"+tt.name, func(t *testing.T) {
			beforeFunctions := u.Functions()
			beforeFrames := u.Frames()
			err := u.Push(tt.frame)
			afterFunctions := u.Functions()
			afterFrames := u.Frames()
			assertErrorCode(t, err, tt.want)
			if !reflect.DeepEqual(beforeFunctions, afterFunctions) || !reflect.DeepEqual(beforeFrames, afterFrames) {
				t.Fatalf("rejected push changed state: before=%+v/%+v after=%+v/%+v", beforeFunctions, beforeFrames, afterFunctions, afterFrames)
			}
			t.Logf("input=Push frame=%+v output=%v basis=snapshots before and after rejection are equal", tt.frame, err)
		})
	}

	throwCases := []struct {
		name  string
		typ   int
		setup func(*testing.T, *Unwinder)
		want  error
	}{
		{"non-positive type", 0, func(*testing.T, *Unwinder) {}, ErrInvalidThrownType},
		{"empty stack before lookup", 1, func(*testing.T, *Unwinder) {}, ErrEmptyStack},
		{"uncaught type", 9, func(t *testing.T, u *Unwinder) {
			pushForTest(t, u, Frame{Function: "valid", PC: 1})
		}, ErrUncaughtException},
	}

	for _, tt := range throwCases {
		t.Run("throw/"+tt.name, func(t *testing.T) {
			local := New()
			registerForTest(t, local, "valid", []Entry{{Start: 10, End: 20, Handler: 30, Type: 2}})
			beforeFunctions := local.Functions()
			tt.setup(t, local)
			beforeFrames := local.Frames()
			result, err := local.Throw(tt.typ)
			afterFunctions := local.Functions()
			afterFrames := local.Frames()
			assertErrorCode(t, err, tt.want)
			if result != (ThrowResult{}) || !reflect.DeepEqual(beforeFunctions, afterFunctions) || !reflect.DeepEqual(beforeFrames, afterFrames) {
				t.Fatalf("rejected throw changed output/state: result=%+v before=%+v/%+v after=%+v/%+v", result, beforeFunctions, beforeFrames, afterFunctions, afterFrames)
			}
			t.Logf("input=Throw type=%d frames=%+v output={result=%+v error=%v} basis=rejected throw preserves exact snapshots", tt.typ, beforeFrames, result, err)
		})
	}
}

type loggedOp struct {
	kind  string
	name  string
	frame Frame
	typ   int
	table []Entry
}

type replayResult struct {
	registerOK bool
	pushOK     bool
	throw      ThrowResult
	throwErr   string
	frames     []Frame
}

func replayOperations(t *testing.T, ops []loggedOp) []replayResult {
	t.Helper()
	u := New()
	results := make([]replayResult, len(ops))

	for index, op := range ops {
		result := replayResult{}
		switch op.kind {
		case "register":
			result.registerOK = u.Register(op.name, op.table) == nil
		case "push":
			result.pushOK = u.Push(op.frame) == nil
		case "throw":
			thrown, err := u.Throw(op.typ)
			result.throw = thrown
			if err != nil {
				result.throwErr = err.Error()
			}
		default:
			t.Fatalf("unknown operation %q", op.kind)
		}
		result.frames = u.Frames()
		results[index] = result
	}

	return results
}

func TestConcurrentRegistrationAndReplay(t *testing.T) {
	const goroutines = 32
	u := New()
	entries := []Entry{{Start: 0, End: 10, Handler: 20, Type: 0}}
	registerStart := make(chan struct{})
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-registerStart
			errs <- u.Register("same", entries)
		}()
	}
	close(registerStart)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		assertErrorCode(t, err, ErrFunctionAlreadyExists)
	}
	if successes != 1 {
		t.Fatalf("successful concurrent registrations = %d, want 1", successes)
	}

	ops := []loggedOp{
		{kind: "register", name: "same", table: entries},
		{kind: "push", frame: Frame{Function: "same", PC: 5}},
		{kind: "throw", typ: 7},
	}
	first := replayOperations(t, ops)
	second := replayOperations(t, ops)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay results differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	t.Logf("input=%+v output=%+v basis=exact operation log replays to identical result and frame snapshots", ops, first)
}

func TestConcurrentPushThrowAndQuery(t *testing.T) {
	u := New()
	registerForTest(t, u, "worker", []Entry{{Start: 1, End: 10, Handler: 100, Type: 0}})

	const goroutines = 16
	pushStart := make(chan struct{})
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-pushStart
			if err := u.Push(Frame{Function: "worker", PC: 5}); err != nil {
				t.Errorf("Push: %v", err)
				return
			}
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-pushStart
			for range goroutines {
				frames := u.Frames()
				for _, frame := range frames {
					if frame.Function != "worker" || frame.PC < 0 {
						t.Errorf("query observed invalid frame %+v", frame)
					}
				}
			}
		}()
	}

	close(pushStart)
	wg.Wait()

	if before := u.Frames(); len(before) != goroutines {
		t.Fatalf("stack after concurrent pushes = %d, want %d", len(before), goroutines)
	}

	throwStart := make(chan struct{})
	successes := make(chan ThrowResult, goroutines)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-throwStart
			result, err := u.Throw(1)
			if err != nil {
				if !errors.Is(err, ErrUncaughtException) {
					t.Errorf("Throw: %v", err)
				}
				return
			}
			if result.HandlerPC != 100 {
				t.Errorf("HandlerPC = %d, want 100", result.HandlerPC)
				return
			}
			successes <- result
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-throwStart
			for range goroutines {
				for _, frame := range u.Frames() {
					if frame.Function != "worker" || (frame.PC != 5 && frame.PC != 100) {
						t.Errorf("throw-phase query observed invalid frame %+v", frame)
					}
				}
			}
		}()
	}

	close(throwStart)
	wg.Wait()
	close(successes)

	if len(successes) != goroutines {
		t.Fatalf("successful concurrent throws = %d, want %d", len(successes), goroutines)
	}
	frames := u.Frames()
	if len(frames) != 1 || frames[0] != (Frame{Function: "worker", PC: 100}) {
		t.Fatalf("final frames = %+v, want one worker frame at pc 100", frames)
	}
	t.Logf("input=%d concurrent pushes then %d concurrent throws plus queries output=%+v basis=linearized throws successively catch each exposed frame until one remains", goroutines, goroutines, frames)
}
