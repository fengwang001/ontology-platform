package ontology

import (
	"math/rand"
	"strings"
	"testing"
)

type modelTrace struct {
	kind   string
	handle Handle
	time   Time
}

type modelTask struct {
	handle Handle
	source Source
	enqAt  Time
	seq    uint64
	depth  int
	idle   Handle
	cancel bool
}

type modelMicro struct{ handle Handle }
type modelFrame struct {
	handle Handle
	cancel bool
	epoch  uint64
}
type modelIdle struct {
	handle Handle
	due    Time
	timed  bool
	cancel bool
}

type naiveModel struct {
	now, frame, interval, minDelay, clamp Time
	limit, threshold                      int
	next                                  Handle
	seq                                   uint64
	tasks                                 []modelTask
	micros                                []modelMicro
	frames                                []modelFrame
	idles                                 []modelIdle
	dirty                                 bool
	traces                                []modelTrace
	idleEpoch                             uint64
}

type randomAction struct {
	kind      int
	source    Source
	delay     Time
	timeout   Time
	advance   Time
	cancelReg int
}

const (
	raTask = iota
	raTimer
	raMicro
	raFrame
	raIdle
	raDirty
	raCancel
)

func TestRandomOperationSequenceAgainstNaiveModel(t *testing.T) {
	config := Config{FrameInterval: 5, StarvationLimit: 2, MinTimerDelay: 0, NestedTimerThreshold: 3, ClampedTimerDelay: 4}
	random := rand.New(rand.NewSource(1520))
	for iteration := 0; iteration < 100; iteration++ {
		actions := generateRandomActions(random)
		want := runNaiveModel(config, actions)
		got := runActualRandomModel(t, config, actions, iteration)
		if want != got {
			t.Fatalf("iteration %d\nnaive:  %s\nactual: %s", iteration, want, got)
		}
	}
}

func generateRandomActions(random *rand.Rand) []randomAction {
	actions := make([]randomAction, 80)
	registrations := 0
	kinds := []int{raTask, raMicro, raFrame, raDirty}
	for index := range actions {
		kind := kinds[random.Intn(len(kinds))]
		actions[index] = randomAction{
			kind:      kind,
			source:    Source(random.Intn(sourceCount)),
			delay:     Time(random.Intn(9)),
			timeout:   Time(1 + random.Intn(8)),
			advance:   Time(random.Intn(2)),
			cancelReg: random.Intn(registrations + 1),
		}
		if kind != raCancel {
			registrations++
		}
	}
	return actions
}

func runNaiveModel(config Config, actions []randomAction) string {
	model := naiveModel{
		now:       0,
		frame:     config.FrameInterval,
		interval:  config.FrameInterval,
		minDelay:  config.MinTimerDelay,
		clamp:     config.ClampedTimerDelay,
		limit:     config.StarvationLimit,
		threshold: config.NestedTimerThreshold,
	}
	var regHandles []Handle
	var skips [sourceCount]int
	for _, action := range actions {
		var handle Handle
		switch action.kind {
		case raTask, raTimer, raMicro, raFrame, raIdle:
			handle = model.alloc()
		case raCancel:
			if action.cancelReg < len(regHandles) {
				model.cancel(regHandles[action.cancelReg])
			}
		}
		switch action.kind {
		case raTask:
			model.tasks = append(model.tasks, modelTask{handle: handle, source: action.source, enqAt: model.now, seq: model.sequence()})
			regHandles = append(regHandles, handle)
		case raTimer:
			delay := action.delay
			if delay < model.minDelay {
				delay = model.minDelay
			}
			model.tasks = append(model.tasks, modelTask{handle: handle, source: SourceTimer, enqAt: model.now + delay, seq: model.sequence(), depth: 1})
			regHandles = append(regHandles, handle)
		case raMicro:
			model.micros = append(model.micros, modelMicro{handle: handle})
			regHandles = append(regHandles, handle)
		case raFrame:
			model.frames = append(model.frames, modelFrame{handle: handle, epoch: model.idleEpoch})
			regHandles = append(regHandles, handle)
		case raIdle:
			model.idles = append(model.idles, modelIdle{handle: handle, due: model.now + action.timeout, timed: true})
			regHandles = append(regHandles, handle)
		case raDirty:
			model.dirty = true
		}

		target := model.now + action.advance
		for {
			model.advanceClock(target)
			index := model.pickTask(skips[:])
			if index >= 0 {
				item := model.tasks[index]
				model.tasks = append(model.tasks[:index], model.tasks[index+1:]...)
				model.markSkipped(item.handle, skips[:])
				kind := EventTask
				if item.source == SourceTimer {
					kind = EventTimer
				} else if item.idle != 0 {
					kind = EventIdleTiming
				}
				traceHandle := item.handle
				if item.idle != 0 {
					traceHandle = item.idle
				}
				model.trace(kind, traceHandle, model.now)
				model.taskGenerated(item)
				model.drainMicros()
				if model.now >= model.frame && model.renderRequested() && !model.hasTaskUntil(model.frame) {
					model.render()
				}
				continue
			}
			model.drainMicros()
			if model.now >= model.frame && model.renderRequested() && !model.hasTaskUntil(model.frame) {
				model.render()
				continue
			}
			if model.canIdle(target) {
				model.idlePeriod()
				continue
			}
			if model.now == target {
				break
			}
			model.now = target
		}
	}
	return compactNaive(model.traces)
}

func (model *naiveModel) alloc() Handle    { model.next++; return model.next }
func (model *naiveModel) sequence() uint64 { model.seq++; return model.seq }
func (model *naiveModel) trace(kind string, handle Handle, time Time) {
	model.traces = append(model.traces, modelTrace{kind: kind, handle: handle, time: time})
}

func (model *naiveModel) cancel(handle Handle) {
	for i := range model.tasks {
		if model.tasks[i].handle == handle {
			model.tasks[i].cancel = true
		}
	}
	for i := range model.frames {
		if model.frames[i].handle == handle {
			model.frames[i].cancel = true
		}
	}
	for i := range model.idles {
		if model.idles[i].handle == handle {
			model.idles[i].cancel = true
		}
	}
	for i := range model.micros {
		if model.micros[i].handle == handle {
			model.micros = append(model.micros[:i], model.micros[i+1:]...)
			return
		}
	}
}

func (model *naiveModel) activeTasks() []int {
	var ids []int
	for i, item := range model.tasks {
		if !item.cancel && item.enqAt <= model.now {
			ids = append(ids, i)
		}
	}
	return ids
}

func (model *naiveModel) hasTaskUntil(deadline Time) bool {
	for _, item := range model.tasks {
		if !item.cancel && item.enqAt <= deadline {
			return true
		}
	}
	return false
}

func (model *naiveModel) pickTask(skips []int) int {
	ids := model.activeTasks()
	force := -1
	for _, id := range ids {
		item := model.tasks[id]
		if skips[item.source] >= model.limit && (force < 0 || model.earlier(item, model.tasks[force])) {
			force = id
		}
	}
	if force >= 0 {
		return force
	}
	for source := SourceUser; source < sourceCount; source++ {
		for _, id := range ids {
			if model.tasks[id].source == source {
				return id
			}
		}
	}
	return -1
}

func (model *naiveModel) earlier(left, right modelTask) bool {
	return left.enqAt < right.enqAt || left.enqAt == right.enqAt && left.source < right.source
}

func (model *naiveModel) markSkipped(chosen Handle, skips []int) {
	heads := map[Source]modelTask{}
	for _, id := range model.activeTasks() {
		item := model.tasks[id]
		if old, ok := heads[item.source]; !ok || item.seq < old.seq {
			heads[item.source] = item
		}
	}
	for source := SourceUser; source < sourceCount; source++ {
		item, ok := heads[source]
		if !ok {
			skips[source] = 0
		} else if item.handle == chosen {
			skips[source] = 0
		} else if skips[source] < model.limit {
			skips[source]++
		}
	}
}

func (model *naiveModel) advanceClock(target Time) {
	for model.now > model.frame && !model.renderRequested() && !model.activeIdle() {
		model.frame = (model.now/model.interval + 1) * model.interval
	}
	if len(model.activeTasks()) > 0 || len(model.micros) > 0 {
		return
	}
	next := target
	if model.renderRequested() && model.frame < next {
		next = model.frame
	}
	for _, item := range model.tasks {
		if !item.cancel && item.enqAt > model.now && item.enqAt < next {
			next = item.enqAt
		}
	}
	for _, item := range model.idles {
		if !item.cancel && item.timed && item.due > model.now && item.due < next {
			next = item.due
		}
	}
	if next > model.now {
		model.now = next
	}
	for i := range model.idles {
		item := &model.idles[i]
		if !item.cancel && item.timed && item.due <= model.now {
			original := item.handle
			model.tasks = append(model.tasks, modelTask{handle: model.alloc(), source: SourceInternal, enqAt: item.due, seq: model.sequence(), idle: original})
			item.cancel = true
		}
	}
}

func (model *naiveModel) taskGenerated(item modelTask) {
	if item.handle%17 == 0 && item.handle != 0 {
		newHandle := model.alloc()
		model.micros = append(model.micros, modelMicro{handle: newHandle})
	}
	if item.handle%23 == 0 {
		newHandle := model.alloc()
		model.tasks = append(model.tasks, modelTask{handle: newHandle, source: item.source, enqAt: model.now, seq: model.sequence(), depth: item.depth})
	}
}

func (model *naiveModel) drainMicros() {
	for len(model.micros) > 0 {
		item := model.micros[0]
		model.micros = model.micros[1:]
		model.trace(EventMicrotask, item.handle, model.now)
		if item.handle%31 == 0 && item.handle != 0 {
			model.micros = append(model.micros, modelMicro{handle: model.alloc()})
		}
	}
}

func (model *naiveModel) renderRequested() bool {
	for _, item := range model.frames {
		if !item.cancel {
			return true
		}
	}
	return model.dirty
}

func (model *naiveModel) render() {
	now := model.now
	model.frame = (now/model.interval + 1) * model.interval
	callbacks := append([]modelFrame(nil), model.frames...)
	model.frames = nil
	model.dirty = false
	model.trace(EventRender, 0, now)
	for _, item := range callbacks {
		if item.cancel {
			continue
		}
		model.trace(EventFrame, item.handle, now)
		model.drainMicros()
	}
}

func (model *naiveModel) activeIdle() bool {
	for _, item := range model.idles {
		if !item.cancel {
			return true
		}
	}
	return false
}

func (model *naiveModel) canIdle(target Time) bool {
	return len(model.activeTasks()) == 0 && len(model.micros) == 0 && !model.renderRequested() &&
		model.now < model.frame && model.frame <= target && model.activeIdle()
}

func (model *naiveModel) idlePeriod() {
	epoch := uint64(1)
	model.idleEpoch = epoch
	count := len(model.idles)
	for i := 0; i < count; i++ {
		if model.idles[i].cancel {
			continue
		}
		model.idles[i].cancel = true
		model.trace(EventIdle, model.idles[i].handle, model.now)
		model.drainMicros()
		if len(model.activeTasks()) > 0 || model.renderRequested() {
			model.idleEpoch = 0
			return
		}
	}
	model.idleEpoch = 0
}

func compactNaive(events []modelTrace) string {
	var b strings.Builder
	for i, event := range events {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(event.kind)
		b.WriteByte(':')
		b.WriteString(itoa(int(event.handle)))
		b.WriteByte('@')
		b.WriteString(itoa(int(event.time)))
	}
	return b.String()
}

func compactActual(events []TraceEvent) string {
	var b strings.Builder
	for i, event := range events {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(event.Type)
		b.WriteByte(':')
		b.WriteString(itoa(int(event.Handle)))
		b.WriteByte('@')
		b.WriteString(itoa(int(event.Time)))
	}
	return b.String()
}

type testLogger struct{ t *testing.T }

func (logger testLogger) Logf(format string, args ...any) {
	logger.t.Helper()
	logger.t.Logf(format, args...)
}

func runActualRandomModel(t *testing.T, config Config, actions []randomAction, iteration int) string {
	t.Helper()
	loop, err := NewLoop(0, config)
	if err != nil {
		t.Fatal(err)
	}
	loop.SetLogger(testLogger{t: t})
	regHandles := []Handle{}
	var all []TraceEvent
	for index, action := range actions {
		var handle Handle
		switch action.kind {
		case raTask:
			handle, _ = loop.EnqueueTask(action.source, randomTaskCallback(action.source))
			regHandles = append(regHandles, handle)
		case raTimer:
			handle, _ = loop.SetTimeout(action.delay, randomTaskCallback(SourceTimer))
			regHandles = append(regHandles, handle)
		case raMicro:
			handle, _ = loop.EnqueueMicrotask(randomMicroCallback())
			regHandles = append(regHandles, handle)
		case raFrame:
			handle, _ = loop.RequestFrame(func(*Context, Time) {})
			regHandles = append(regHandles, handle)
		case raIdle:
			handle, _ = loop.RequestIdle(action.timeout, func(*Context, Deadline) {})
			regHandles = append(regHandles, handle)
		case raDirty:
			loop.MarkDirty()
		case raCancel:
			if action.cancelReg < len(regHandles) {
				_ = loop.Cancel(regHandles[action.cancelReg])
			}
		}
		target := loop.Now() + action.advance
		t.Logf("advance input: case=%d action=%d kind=%d source=%d now=%d target=%d", iteration, index, action.kind, action.source, loop.Now(), target)
		events, err := loop.Advance(target)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("advance output: case=%d action=%d trace=%s decision=matched-model-prefix", iteration, index, compactActual(events))
		all = append(all, events...)
	}
	return compactActual(all)
}

func randomTaskCallback(source Source) TaskFn {
	return func(ctx *Context) {
		if ctx.Handle()%17 == 0 {
			ctx.EnqueueMicrotask(randomMicroCallback())
		}
		if ctx.Handle()%23 == 0 {
			ctx.EnqueueTask(source, randomTaskCallback(source))
		}
	}
}

func randomMicroCallback() MicrotaskFn {
	return func(ctx *Context) {
		if ctx.Handle()%31 == 0 {
			ctx.EnqueueMicrotask(randomMicroCallback())
		}
	}
}
