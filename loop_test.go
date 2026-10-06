package ontology

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{FrameInterval: 10, StarvationLimit: 3, MinTimerDelay: 1, NestedTimerThreshold: 4, ClampedTimerDelay: 10}
}

func traceIDs(events []TraceEvent) string {
	var builder strings.Builder
	for index, event := range events {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(event.Type)
		builder.WriteByte(':')
		builder.WriteString(itoa(int(event.Handle)))
	}
	return builder.String()
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}

func TestMicrotasksDrainDeeplyBeforeNextTask(t *testing.T) {
	loop, err := NewLoop(0, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var order []Handle
	var first Handle
	second, _ := loop.EnqueueTask(SourceMessage, func(ctx *Context) { order = append(order, 20) })
	first, _ = loop.EnqueueTask(SourceUser, func(ctx *Context) {
		order = append(order, first)
		ctx.EnqueueMicrotask(func(ctx *Context) {
			order = append(order, 11)
			ctx.EnqueueMicrotask(func(ctx *Context) {
				order = append(order, 12)
				ctx.EnqueueMicrotask(func(ctx *Context) { order = append(order, 13) })
			})
		})
	})
	_ = second
	events, err := loop.Advance(0)
	if err != nil {
		t.Fatal(err)
	}
	got := traceIDs(events)
	want := "task:2,microtask:3,microtask:4,microtask:5,task:1"
	if got != want {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

func TestPanicsDoNotStopFollowingCallbacks(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	_, _ = loop.EnqueueTask(SourceUser, func(ctx *Context) { panic("boom") })
	_, _ = loop.EnqueueMicrotask(func(ctx *Context) { panic("micro boom") })
	_, _ = loop.EnqueueTask(SourceMessage, func(ctx *Context) {})
	if _, err := loop.Advance(0); err != nil {
		t.Fatal(err)
	}
	reports := loop.Errors(0, 0)
	if len(reports) != 2 {
		t.Fatalf("errors = %d, want 2", len(reports))
	}
	if reports[0].Handle != 1 || reports[1].Handle != 2 {
		t.Fatalf("error handles = %d,%d", reports[0].Handle, reports[1].Handle)
	}
}

func TestFrameExactAndMultipleMissedBoundariesCoalesce(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	var times []Time
	_, _ = loop.RequestFrame(func(ctx *Context, now Time) { times = append(times, now) })
	_, _ = loop.EnqueueTask(SourceMessage, func(ctx *Context) {})
	events, err := loop.Advance(25)
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 1 || times[0] != 10 {
		t.Fatalf("frame times = %v", times)
	}
	count := 0
	for _, event := range events {
		if event.Type == EventRender {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("renders = %d, want 1: %#v", count, events)
	}
}

func TestIdleZeroRemainingDoesNotRun(t *testing.T) {
	loop, _ := NewLoop(9, testConfig())
	ran := false
	_, _ = loop.RequestIdle(0, func(ctx *Context, deadline Deadline) {
		ran = true
	})
	if _, err := loop.Advance(10); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("idle callback ran with zero remaining time")
	}
}

func TestIdleTimeoutAndIdleOpportunityAtSameInstant(t *testing.T) {
	config := testConfig()
	config.FrameInterval = 10
	loop, _ := NewLoop(0, config)
	var events []string
	_, _ = loop.RequestIdle(5, func(ctx *Context, deadline Deadline) {
		if deadline.Timeout {
			events = append(events, "timeout")
			return
		}
		events = append(events, "idle")
	})
	if _, err := loop.Advance(5); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "timeout" {
		t.Fatalf("events = %v, want timeout-only", events)
	}
}

func TestTaskEndingExactlyOnFrameBoundaryRenders(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	var frameTime Time = -1
	_, _ = loop.RequestFrame(func(ctx *Context, now Time) { frameTime = now })
	_, _ = loop.EnqueueTask(SourceMessage, func(ctx *Context) {
		ctx.EnqueueMicrotask(func(ctx *Context) {})
	})
	events, _ := loop.Advance(10)
	if frameTime != 10 {
		t.Fatalf("frame time = %d, events = %s", frameTime, traceIDs(events))
	}
}

func TestInvalidRejectionsAreAtomicAndOrdered(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	if _, err := loop.SetTimeout(-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("error = %v", err)
	}
	if _, err := loop.Advance(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative target error = %v", err)
	}
	if err := loop.Cancel(99); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("unknown handle error = %v", err)
	}
	handle, _ := loop.EnqueueTask(SourceUser, func(ctx *Context) {})
	if err := loop.Cancel(handle); err != nil {
		t.Fatal(err)
	}
	if err := loop.Cancel(handle); !errors.Is(err, ErrDuplicateCancel) {
		t.Fatalf("duplicate cancel error = %v", err)
	}
}

func TestUserPriorityAndStarvationLimitExact(t *testing.T) {
	config := testConfig()
	config.StarvationLimit = 3
	loop, _ := NewLoop(0, config)
	var order []Source
	for _, source := range []Source{SourceNetwork, SourceMessage, SourceInternal} {
		source := source
		_, _ = loop.EnqueueTask(source, func(ctx *Context) { order = append(order, source) })
	}
	_, _ = loop.EnqueueTask(SourceUser, func(ctx *Context) { order = append(order, SourceUser) })
	if _, err := loop.Advance(0); err != nil {
		t.Fatal(err)
	}
	want := []Source{SourceUser, SourceNetwork, SourceMessage, SourceInternal}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}

	loop, _ = NewLoop(0, config)
	order = nil
	for round := 0; round < 3; round++ {
		_, _ = loop.EnqueueTask(SourceUser, func(ctx *Context) {
			order = append(order, SourceUser)
			ctx.EnqueueTask(SourceUser, func(ctx *Context) { order = append(order, SourceUser) })
		})
	}
	starved, _ := loop.EnqueueTask(SourceNetwork, func(ctx *Context) { order = append(order, SourceNetwork) })
	_, _ = loop.Advance(0)
	if order[3] != SourceNetwork {
		t.Fatalf("starved handle %d did not run after exact limit, order=%v", starved, order)
	}
}

func TestCanceledTaskDoesNotConsumeStarvationRound(t *testing.T) {
	config := testConfig()
	config.StarvationLimit = 1
	loop, _ := NewLoop(0, config)
	canceled, _ := loop.EnqueueTask(SourceNetwork, func(ctx *Context) {})
	_, _ = loop.EnqueueTask(SourceUser, func(ctx *Context) {})
	message, _ := loop.EnqueueTask(SourceMessage, func(ctx *Context) {})
	if err := loop.Cancel(canceled); err != nil {
		t.Fatal(err)
	}
	events, _ := loop.Advance(0)
	for _, event := range events {
		if event.Handle == canceled {
			t.Fatalf("canceled task appeared in trace: %#v", event)
		}
	}
	if events[len(events)-1].Handle != message {
		t.Fatalf("last event = %#v, want handle %d", events[len(events)-1], message)
	}
}

func TestTimerNestedThresholdExactAndCancellation(t *testing.T) {
	config := Config{FrameInterval: 100, StarvationLimit: 3, MinTimerDelay: 0, NestedTimerThreshold: 2, ClampedTimerDelay: 10}
	loop, _ := NewLoop(0, config)
	var times []Time
	var root Handle
	root, _ = loop.SetTimeout(0, func(ctx *Context) {
		times = append(times, ctx.Now())
		ctx.SetTimeout(0, func(ctx *Context) {
			times = append(times, ctx.Now())
			ctx.SetTimeout(0, func(ctx *Context) {
				times = append(times, ctx.Now())
				ctx.SetTimeout(0, func(ctx *Context) { times = append(times, ctx.Now()) })
			})
		})
	})
	_, _ = loop.Advance(100)
	wantTimes := []Time{0, 0, 0, 10}
	if len(times) != len(wantTimes) {
		t.Fatalf("times = %v, want %v (root=%d)", times, wantTimes, root)
	}
	for index := range wantTimes {
		if times[index] != wantTimes[index] {
			t.Fatalf("times = %v, want %v", times, wantTimes)
		}
	}

	loop, _ = NewLoop(0, config)
	handle, _ := loop.SetTimeout(5, func(ctx *Context) { t.Fatal("canceled timer executed") })
	if err := loop.Cancel(handle); err != nil {
		t.Fatal(err)
	}
	events, _ := loop.Advance(10)
	for _, event := range events {
		if event.Handle == handle {
			t.Fatalf("canceled timer traced: %#v", event)
		}
	}
}

func TestFrameCallbacksDeferNewFrameAndMicrotaskAfterEach(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	var order []string
	first, _ := loop.RequestFrame(func(ctx *Context, now Time) {
		order = append(order, "frame1")
		ctx.RequestFrame(func(ctx *Context, now Time) { order = append(order, "frame2") })
		ctx.EnqueueMicrotask(func(ctx *Context) { order = append(order, "micro1") })
	})
	second, _ := loop.RequestFrame(func(ctx *Context, now Time) { order = append(order, "frame3") })
	_, _ = first, second
	events, _ := loop.Advance(20)
	got := strings.Join(order, ",")
	if got != "frame1,micro1,frame3,frame2" {
		t.Fatalf("order=%s events=%s", got, traceIDs(events))
	}
}

func TestConcurrentEnqueuePreservesObservedOrder(t *testing.T) {
	loop, _ := NewLoop(0, testConfig())
	const count = 100
	handles := make(chan Handle, count)
	var observed sync.Mutex
	var ready, done sync.WaitGroup
	ready.Add(1)
	for index := 0; index < count; index++ {
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Wait()
			observed.Lock()
			defer observed.Unlock()
			handle, _ := loop.EnqueueTask(SourceMessage, func(ctx *Context) {})
			handles <- handle
		}()
	}
	ready.Done()
	done.Wait()
	close(handles)
	var previous Handle
	for handle := range handles {
		if handle <= previous {
			t.Fatalf("handles not increasing: %d after %d", handle, previous)
		}
		previous = handle
	}
}

func BenchmarkSelectionAndMicrotaskCheckpoint(b *testing.B) {
	config := testConfig()
	for _, size := range []int{100, 1000, 10000} {
		b.Run("size-"+itoa(size), func(b *testing.B) {
			loop, _ := NewLoop(0, config)
			for index := 0; index < size; index++ {
				_, _ = loop.EnqueueTask(SourceNetwork, func(*Context) {})
			}
			userHandle, _ := loop.EnqueueTask(SourceUser, func(*Context) {})
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				loop.mu.Lock()
				if chosen := loop.selectTaskLocked(); chosen == nil || chosen.handle != userHandle {
					b.Fatalf("selected unexpected task")
				}
				microHandle := Handle(size + iteration + 1000000)
				loop.state[microHandle] = stateActive
				loop.microtasks.PushBack(&microtask{handle: microHandle})
				loop.runMicrotasksLocked()
				loop.mu.Unlock()
			}
		})
	}
}
