package partition

import (
	"errors"
	"sort"
	"sync"
)

// Infinity is the effective watermark of a finished writer.
const Infinity = int64(^uint64(0) >> 1)

// State of a partition.
type State string

const (
	StateWaiting      State = "waiting"
	StateReady        State = "ready"
	StateInProgress   State = "in_progress"
	StateCommitted    State = "committed"
	StatePendingPatch State = "pending_patch"
	StateCommitFailed State = "commit_failed"
)

// Steps of a normal commit round.
const (
	StepRegisterMetadata = 0
	StepSuccessMarker    = 1
)

var (
	ErrWriterOutOfRange   = errors.New("partition: writer subtask id out of range")
	ErrNegativeEventTime  = errors.New("partition: event time is negative")
	ErrWriterFinished     = errors.New("partition: writer subtask already finished")
	ErrWatermarkRegressed = errors.New("partition: watermark must not regress")
	ErrAlreadyFinished    = errors.New("partition: writer subtask finish repeated")
	ErrPartitionNotFound  = errors.New("partition: partition does not exist")
	ErrNotReady           = errors.New("partition: step not executable: partition not ready")
	ErrBlocked            = errors.New("partition: step not executable: blocked by smaller partition")
	ErrAlreadyCommitted   = errors.New("partition: step not executable: partition already committed")
	ErrCommitFailed       = errors.New("partition: step not executable: partition in commit_failed")
	ErrWrongStep          = errors.New("partition: reported step does not match the executable step")
	ErrNotInCommitFailed  = errors.New("partition: reset requires commit_failed state")
	ErrInvalidConfig      = errors.New("partition: invalid trigger configuration")
)

// Logger is the minimal logging surface used by the trigger.
type Logger interface {
	Printf(format string, args ...any)
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// Options configures a Trigger.
type Options struct {
	Writers        int
	Period         int64
	AllowedRetries int
	Lateness       int64
	Log            Logger
}

// Trigger decides when time partitions are ready and serializes their commits.
type Trigger struct {
	mu      sync.Mutex
	log     Logger
	w       int
	period  int64
	retries int
	late    int64

	reported   []bool
	finished   []bool
	watermark  []int64
	haveGlobal bool
	global     int64

	parts map[int64]*part
}

type part struct {
	id    int64
	state State
	count int64
	step  int
	fails int
	ver   int64
}

func stepName(step int) string {
	switch step {
	case StepRegisterMetadata:
		return "register-metadata"
	case StepSuccessMarker:
		return "write-success-marker"
	default:
		return "unknown"
	}
}

func (p *part) snapshot() Snapshot {
	return Snapshot{
		ID:          p.id,
		State:       p.state,
		Count:       p.count,
		CurrentStep: p.step,
		Failures:    p.fails,
		Version:     p.ver,
	}
}

// recomputeGlobal derives the global watermark (min of all writer watermarks).
// It only exists once every writer has reported (via AdvanceWatermark or Finish).
// Caller holds mu.
func (t *Trigger) recomputeGlobal() {
	mn := Infinity
	for i := 0; i < t.w; i++ {
		if !t.reported[i] {
			t.haveGlobal = false
			return
		}
		if t.finished[i] {
			continue
		}
		if t.watermark[i] < mn {
			mn = t.watermark[i]
		}
	}
	t.haveGlobal = true
	t.global = mn
}

// readyThreshold is (k+1)*P + lateness for partition k.
func (t *Trigger) readyThreshold(id int64) int64 {
	return (id+1)*t.period + t.late
}

// blocksSmaller reports whether a smaller partition prevents id's first step.
// Only committed / pending_patch partitions are out of the way.
// Caller holds mu.
func (t *Trigger) blocksSmaller(id int64) (int64, State) {
	for k := int64(0); k < id; k++ {
		sp := t.parts[k]
		if sp == nil {
			continue // 尚未创建的更小分区不阻塞
		}
		if sp.state != StateCommitted && sp.state != StatePendingPatch {
			return k, sp.state
		}
	}
	return 0, ""
}

// refreshWaiting moves waiting partitions to ready when the global watermark
// reaches their boundary. Ready partitions that become blocked again stay in
// ready but their step is not executable until the blocker clears.
// Caller holds mu.
func (t *Trigger) refreshWaiting() {
	if !t.haveGlobal {
		return
	}
	for k := int64(0); ; k++ {
		sp := t.parts[k]
		if sp == nil {
			break
		}
		if sp.state == StateWaiting && t.global >= t.readyThreshold(k) {
			sp.state = StateReady
			t.log.Printf("partition %d: waiting -> ready, global=%d threshold=%d", k, t.global, t.readyThreshold(k))
		}
	}
}

// Snapshot is an immutable view of a partition.
type Snapshot struct {
	ID          int64
	State       State
	Count       int64
	CurrentStep int
	Failures    int
	Version     int64
}

// New creates a Trigger.
func New(o Options) (*Trigger, error) {
	if o.Writers <= 0 || o.Period <= 0 || o.AllowedRetries <= 0 || o.Lateness < 0 {
		return nil, ErrInvalidConfig
	}
	log := Logger(o.Log)
	if log == nil {
		log = discardLogger{}
	}
	return &Trigger{
		log:       log,
		w:         o.Writers,
		period:    o.Period,
		retries:   o.AllowedRetries,
		late:      o.Lateness,
		reported:  make([]bool, o.Writers),
		finished:  make([]bool, o.Writers),
		watermark: make([]int64, o.Writers),
		parts:     map[int64]*part{},
	}, nil
}

// Write records an event into the partition of eventTime.
func (t *Trigger) Write(writer int, eventTime int64) (partID int64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if writer < 0 || writer >= t.w {
		t.log.Printf("write rejected: input{writer=%d,eventTime=%d} reason=writer-out-of-range (want [0,%d))", writer, eventTime, t.w)
		return 0, ErrWriterOutOfRange
	}
	if eventTime < 0 {
		t.log.Printf("write rejected: input{writer=%d,eventTime=%d} reason=negative-event-time", writer, eventTime)
		return 0, ErrNegativeEventTime
	}
	if t.finished[writer] {
		t.log.Printf("write rejected: input{writer=%d,eventTime=%d} reason=writer-finished", writer, eventTime)
		return 0, ErrWriterFinished
	}
	id := eventTime / t.period
	sp := t.parts[id]
	created := false
	if sp == nil {
		sp = &part{id: id, state: StateWaiting}
		t.parts[id] = sp
		created = true
	}
	sp.count++
	before := sp.state
	if sp.state == StateCommitted {
		sp.state = StatePendingPatch
	}
	t.refreshWaiting()
	t.log.Printf("write: input{writer=%d,eventTime=%d} -> partition=%d created=%t count=%d state=%s(patchFrom=%v)",
		writer, eventTime, id, created, sp.count, sp.state, before == StateCommitted)
	return id, nil
}

// AdvanceWatermark reports a new watermark for a writer.
func (t *Trigger) AdvanceWatermark(writer int, watermark int64) (globalWatermark int64, allReported bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if writer < 0 || writer >= t.w {
		t.log.Printf("advance rejected: input{writer=%d,watermark=%d} reason=writer-out-of-range", writer, watermark)
		return 0, false, ErrWriterOutOfRange
	}
	if t.finished[writer] {
		t.log.Printf("advance rejected: input{writer=%d,watermark=%d} reason=writer-finished", writer, watermark)
		return 0, false, ErrWriterFinished
	}
	if t.reported[writer] && watermark < t.watermark[writer] {
		t.log.Printf("advance rejected: input{writer=%d,watermark=%d} reason=watermark-regressed(current=%d)",
			writer, watermark, t.watermark[writer])
		return 0, false, ErrWatermarkRegressed
	}
	t.reported[writer] = true
	t.watermark[writer] = watermark
	t.recomputeGlobal()
	t.refreshWaiting()
	if t.haveGlobal {
		t.log.Printf("advance: input{writer=%d,watermark=%d} -> global=%d allReported=true; waiting rechecked", writer, watermark, t.global)
	} else {
		t.log.Printf("advance: input{writer=%d,watermark=%d} -> global=absent (not all writers reported)", writer, watermark)
	}
	return t.global, t.haveGlobal, nil
}

// Finish marks a writer as finished; its watermark becomes infinity.
func (t *Trigger) Finish(writer int) (globalWatermark int64, allReported bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if writer < 0 || writer >= t.w {
		t.log.Printf("finish rejected: input{writer=%d} reason=writer-out-of-range", writer)
		return 0, false, ErrWriterOutOfRange
	}
	if t.finished[writer] {
		t.log.Printf("finish rejected: input{writer=%d} reason=already-finished", writer)
		return 0, false, ErrAlreadyFinished
	}
	t.finished[writer] = true
	t.reported[writer] = true
	t.watermark[writer] = Infinity
	t.recomputeGlobal()
	t.refreshWaiting()
	t.log.Printf("finish: input{writer=%d} -> watermark=+Inf global=%v allReported=%v", writer, t.global, t.haveGlobal)
	return t.global, t.haveGlobal, nil
}

// ReportStep reports success or failure of the currently executable step.
func (t *Trigger) ReportStep(partID int64, step int, success bool) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sp := t.parts[partID]
	if sp == nil {
		t.log.Printf("report rejected: input{partition=%d,step=%d,success=%v} reason=partition-not-found", partID, step, success)
		return Snapshot{}, ErrPartitionNotFound
	}
	var expectStep int
	executable := false
	switch sp.state {
	case StateWaiting:
		t.log.Printf("report rejected: partition=%d reason=not-ready state=waiting global=%v", partID, t.globalIfKnown())
		return Snapshot{}, ErrNotReady
	case StateCommitFailed:
		t.log.Printf("report rejected: partition=%d reason=commit-failed (awaiting reset)", partID)
		return Snapshot{}, ErrCommitFailed
	case StateCommitted:
		t.log.Printf("report rejected: partition=%d reason=already-committed (no round open)", partID)
		return Snapshot{}, ErrAlreadyCommitted
	case StateReady:
		if bid, bstate := t.blocksSmaller(partID); bstate != "" {
			t.log.Printf("report rejected: partition=%d reason=blocked by smaller partition=%d state=%s", partID, bid, bstate)
			return Snapshot{}, ErrBlocked
		}
		expectStep = StepRegisterMetadata
		executable = true
	case StateInProgress:
		expectStep = sp.step
		executable = true
	case StatePendingPatch:
		expectStep = StepSuccessMarker
		executable = true
	}
	if !executable {
		t.log.Printf("report rejected: partition=%d reason=not-ready state=%s", partID, sp.state)
		return Snapshot{}, ErrNotReady
	}
	if step != expectStep {
		t.log.Printf("report rejected: partition=%d input{step=%d} reason=wrong-step want=%d(%s)",
			partID, step, expectStep, stepName(expectStep))
		return Snapshot{}, ErrWrongStep
	}

	if !success {
		sp.fails++
		if sp.fails >= t.retries {
			sp.state = StateCommitFailed
			t.log.Printf("report: partition=%d step=%d(%s) result=failure failures=%d -> commit_failed at R=%d",
				partID, step, stepName(step), sp.fails, t.retries)
		} else {
			t.log.Printf("report: partition=%d step=%d(%s) result=failure failures=%d (state stays %s, same step retriable)",
				partID, step, stepName(step), sp.fails, sp.state)
		}
		return sp.snapshot(), nil
	}

	from := sp.state
	switch from {
	case StateReady:
		sp.state = StateInProgress
		sp.step = StepSuccessMarker
		t.log.Printf("report: partition=%d step=%d(%s) result=success -> in_progress, next step=%d(%s)",
			partID, step, stepName(step), sp.step, stepName(sp.step))
	case StateInProgress:
		sp.state = StateCommitted
		sp.fails = 0
		sp.ver++
		t.log.Printf("report: partition=%d step=%d(%s) result=success -> committed version=%d (round complete)",
			partID, step, stepName(step), sp.ver)
	case StatePendingPatch:
		sp.state = StateCommitted
		sp.fails = 0
		sp.ver++
		t.log.Printf("report: partition=%d step=%d(%s) result=success patch round complete -> committed version=%d",
			partID, step, stepName(step), sp.ver)
	}
	return sp.snapshot(), nil
}

// Reset returns a commit_failed partition to its round's start.
func (t *Trigger) Reset(partID int64) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sp := t.parts[partID]
	if sp == nil {
		t.log.Printf("reset rejected: input{partition=%d} reason=partition-not-found", partID)
		return Snapshot{}, ErrPartitionNotFound
	}
	if sp.state != StateCommitFailed {
		t.log.Printf("reset rejected: partition=%d reason=not-in-commit-failed state=%s", partID, sp.state)
		return Snapshot{}, ErrNotInCommitFailed
	}
	sp.fails = 0
	if sp.ver > 0 {
		// 首轮已完成过：失败的是补提交轮，回到该轮第一步（写成功标记）。
		sp.state = StatePendingPatch
		sp.step = StepSuccessMarker
	} else {
		// 首轮失败：回到就绪，从登记元数据重来。
		sp.state = StateReady
		sp.step = StepRegisterMetadata
	}
	t.log.Printf("reset: partition=%d -> %s failures=0 step=%d(%s) version kept=%d",
		partID, sp.state, sp.step, stepName(sp.step), sp.ver)
	return sp.snapshot(), nil
}

// ExecutableStep returns the currently executable step for a partition.
func (t *Trigger) ExecutableStep(partID int64) (step int, ok bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sp := t.parts[partID]
	if sp == nil {
		return 0, false, ErrPartitionNotFound
	}
	switch sp.state {
	case StateReady:
		if _, bstate := t.blocksSmaller(partID); bstate != "" {
			return 0, false, nil
		}
		return StepRegisterMetadata, true, nil
	case StateInProgress:
		return sp.step, true, nil
	case StatePendingPatch:
		return StepSuccessMarker, true, nil
	default:
		return 0, false, nil
	}
}

// SnapshotPartitions returns immutable views of all created partitions in id order.
func (t *Trigger) SnapshotPartitions() []Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]int64, 0, len(t.parts))
	for id := range t.parts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		out = append(out, t.parts[id].snapshot())
	}
	return out
}

// GlobalWatermark returns the global watermark and whether all writers reported.
func (t *Trigger) GlobalWatermark() (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.global, t.haveGlobal
}

func (t *Trigger) globalIfKnown() any {
	if t.haveGlobal {
		return t.global
	}
	return "absent"
}
