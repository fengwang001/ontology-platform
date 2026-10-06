package ontology

import (
	"container/list"
	"errors"
	"sync"
)

type Time int64

type Source int

const (
	SourceUser Source = iota
	SourceTimer
	SourceNetwork
	SourceMessage
	SourceInternal
)

type Handle uint64

type Config struct {
	FrameInterval        Time
	StarvationLimit      int
	MinTimerDelay        Time
	NestedTimerThreshold int
	ClampedTimerDelay    Time
}

type TaskFn func(ctx *Context)
type MicrotaskFn func(ctx *Context)
type FrameFn func(ctx *Context, now Time)
type IdleFn func(ctx *Context, deadline Deadline)

type Context struct {
	loop   *Loop
	depth  int
	handle Handle
}

func (ctx *Context) Now() Time {
	ctx.loop.mu.Lock()
	now := ctx.loop.now
	ctx.loop.mu.Unlock()
	return now
}

func (ctx *Context) Handle() Handle {
	return ctx.handle
}

func (ctx *Context) EnqueueTask(source Source, fn TaskFn) (Handle, error) {
	return ctx.loop.EnqueueTask(source, fn)
}

func (ctx *Context) EnqueueMicrotask(fn MicrotaskFn) (Handle, error) {
	return ctx.loop.enqueueMicrotask(fn, ctx.depth)
}

func (ctx *Context) RequestFrame(fn FrameFn) (Handle, error) {
	return ctx.loop.RequestFrame(fn)
}

func (ctx *Context) RequestIdle(timeout Time, fn IdleFn) (Handle, error) {
	return ctx.loop.RequestIdle(timeout, fn)
}

func (ctx *Context) SetTimeout(delay Time, fn TaskFn) (Handle, error) {
	return ctx.loop.setTimeout(delay, fn, ctx.depth)
}

type Deadline struct {
	Now      Time
	Deadline Time
	Timeout  bool
}

func (deadline Deadline) TimeRemaining() Time {
	return deadline.Deadline - deadline.Now
}

const (
	EventTask       = "task"
	EventMicrotask  = "microtask"
	EventRender     = "render"
	EventFrame      = "frame"
	EventIdle       = "idle"
	EventTimer      = "timer"
	EventIdleTiming = "idle-timeout"
)

type TraceEvent struct {
	Type     string
	Handle   Handle
	Time     Time
	Source   Source
	Depth    int
	Remain   Time
	Deadline Time
}

type ErrorReport struct {
	Time   Time
	Handle Handle
	Err    error
}

type Logger interface {
	Logf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

type Loop struct {
	mu     sync.Mutex
	execMu sync.Mutex

	now    Time
	config Config
	logger Logger
	traces []TraceEvent
	errors []ErrorReport

	nextHandle Handle
	nextSeq    uint64
	queues     [sourceCount]sourceQueue
	microtasks *list.List
	frames     *list.List
	idles      *list.List
	timers     timerHeap
	state      map[Handle]entryState
	timerByID  map[Handle]*timerEntry
	frameByID  map[Handle]*list.Element
	idleByID   map[Handle]*list.Element
	taskByID   map[Handle]*list.Element
	microByID  map[Handle]*list.Element
	dirty      bool
	nextFrame  Time
	idleEpoch  uint64
}

func NewLoop(now Time, config Config) (*Loop, error) {
	if config.FrameInterval == 0 {
		config.FrameInterval = 1
	}
	if config.StarvationLimit == 0 {
		config.StarvationLimit = 3
	}
	if now < 0 {
		return nil, invalidArgument("initial time must not be negative")
	}
	if config.FrameInterval <= 0 {
		return nil, invalidArgument("frame interval must be positive")
	}
	if config.StarvationLimit <= 0 {
		return nil, invalidArgument("starvation limit must be positive")
	}
	if config.MinTimerDelay < 0 || config.ClampedTimerDelay < 0 {
		return nil, invalidArgument("timer delays must not be negative")
	}
	if config.NestedTimerThreshold < 0 {
		return nil, invalidArgument("nested timer threshold must not be negative")
	}
	loop := &Loop{
		now:        now,
		config:     config,
		logger:     nopLogger{},
		microtasks: list.New(),
		frames:     list.New(),
		idles:      list.New(),
		state:      make(map[Handle]entryState),
		timerByID:  make(map[Handle]*timerEntry),
		frameByID:  make(map[Handle]*list.Element),
		idleByID:   make(map[Handle]*list.Element),
		taskByID:   make(map[Handle]*list.Element),
		microByID:  make(map[Handle]*list.Element),
	}
	for source := SourceUser; source < sourceCount; source++ {
		loop.queues[source] = newSourceQueue()
	}
	if config.FrameInterval > 0 {
		loop.nextFrame = nextBoundary(now, config.FrameInterval)
	}
	return loop, nil
}

func (loop *Loop) SetLogger(logger Logger) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if logger == nil {
		loop.logger = nopLogger{}
	} else {
		loop.logger = logger
	}
}

func (loop *Loop) Now() Time {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.now
}

func IsLoopError(err error, kind ErrorKind) bool {
	var target *LoopError
	return errors.As(err, &target) && target.Kind == kind
}
