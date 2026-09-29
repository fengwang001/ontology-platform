package scheduler

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
)

// Job 描述一个待调度作业。
type Job struct {
	ID       string
	Nodes    int64
	Duration int64
}

// RunningJob 是运行集合中的一条快照记录。
type RunningJob struct {
	ID        string
	Nodes     int64
	StartTime int64
	EndTime   int64 // 预估结束时刻
}

// QueueEntry 是等待队列中的一条快照记录。
type QueueEntry struct {
	ID       string
	Nodes    int64
	Duration int64
	// Shadow 是该作业成为队首时为它保留的最早可行启动时刻（实际启动时刻的上界）。
	Shadow int64
	// Surplus 是影子时刻满足队首后仍富余的节点数。
	Surplus int64
}

// Snapshot 是某一时刻调度器状态的确定性快照。
type Snapshot struct {
	Now     int64
	Running []RunningJob
	Queue   []QueueEntry
}

// Scheduler 是多节点作业的保留加回填调度器。
type Scheduler struct {
	mu      sync.Mutex
	nodes   int64
	clock   Clock
	logW    io.Writer
	queued  []*jobState
	running map[string]*jobState
}

type jobState struct {
	id       string
	nodes    int64
	duration int64
	start    int64
	end      int64
	shadow   int64
	surplus  int64
}

// New 创建调度器；start 为时钟初始时刻，logW 为 nil 时不打印判定日志。
func New(n int64, start int64, logW io.Writer) (*Scheduler, error) {
	if n <= 0 {
		return nil, ErrInvalidNodes
	}
	s := &Scheduler{
		nodes:   n,
		clock:   newInjectedClock(start),
		logW:    logW,
		running: make(map[string]*jobState),
	}
	s.logf("input", "New nodes=%d start=%d", n, start)
	s.logf("output", "New ok nodes=%d now=%d", n, start)
	return s, nil
}

// Submit 提交一个作业并立即触发一次调度。
func (s *Scheduler) Submit(j Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("input", "Submit id=%q nodes=%d duration=%d now=%d", j.ID, j.Nodes, j.Duration, s.clock.Now())
	if j.Nodes <= 0 || j.Nodes > s.nodes {
		s.logf("reject", "Submit id=%q reason=invalid-job nodes=%d cluster=%d", j.ID, j.Nodes, s.nodes)
		s.logf("output", "Submit id=%q rejected", j.ID)
		return
	}
	if j.Duration <= 0 {
		s.logf("reject", "Submit id=%q reason=invalid-duration duration=%d", j.ID, j.Duration)
		s.logf("output", "Submit id=%q rejected", j.ID)
		return
	}
	if _, dup := s.running[j.ID]; dup {
		s.logf("reject", "Submit id=%q reason=duplicate-id where=running", j.ID)
		s.logf("output", "Submit id=%q rejected", j.ID)
		return
	}
	for _, q := range s.queued {
		if q.id == j.ID {
			s.logf("reject", "Submit id=%q reason=duplicate-id where=queue", j.ID)
			s.logf("output", "Submit id=%q rejected", j.ID)
			return
		}
	}
	s.queued = append(s.queued, &jobState{id: j.ID, nodes: j.Nodes, duration: j.Duration})
	s.logf("accepted", "Submit id=%q enqueued queueLen=%d", j.ID, len(s.queued))
	s.scheduleLocked("submit:" + j.ID)
	s.logf("output", "Submit id=%q done %s", j.ID, s.stateSummary())
}

// Finish 提前结束一个运行中的作业并立即触发重算。
func (s *Scheduler) Finish(id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("input", "Finish id=%q at=%d now=%d", id, at, s.clock.Now())
	if at < s.clock.Now() {
		s.logf("reject", "Finish id=%q reason=clock-rewind at=%d now=%d", id, at, s.clock.Now())
		return ErrClockRewind
	}
	j, ok := s.running[id]
	if !ok {
		s.logf("reject", "Finish id=%q reason=job-not-running", id)
		return ErrJobNotRunning
	}
	// 校验全部通过后才允许改变状态：先推进时钟（可能强制结束其他作业），再移除目标作业。
	if at > s.clock.Now() {
		if c, ok := s.clock.(*injectedClock); ok {
			c.now = at
		}
		s.expireLocked("finish-clock:"+id, strconv.FormatInt(at, 10))
	}
	if cur, still := s.running[id]; still {
		j = cur
		s.removeRunningLocked(j)
		s.logf("event", "Finish id=%q ended-early start=%d end=%d at=%d released=%d",
			id, j.start, j.end, at, j.nodes)
	}
	s.scheduleLocked("finish:" + id)
	s.logf("output", "Finish id=%q done %s", id, s.stateSummary())
	return nil
}

// Advance 推进时钟；到预估时长的作业被强制结束并触发调度。
func (s *Scheduler) Advance(t int64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("input", "Advance to=%d now=%d", t, s.clock.Now())
	if t < s.clock.Now() {
		s.logf("reject", "Advance reason=clock-rewind to=%d now=%d", t, s.clock.Now())
		return Snapshot{}, ErrClockRewind
	}
	if c, ok := s.clock.(*injectedClock); ok && t > c.now {
		c.now = t
	}
	s.scheduleLocked("advance:" + strconv.FormatInt(t, 10))
	snap := s.snapshotLocked()
	s.logf("output", "Advance to=%d done %s", t, s.stateSummary())
	return snap, nil
}

// Query 返回当前状态快照（同样会处理到点的强制结束）。
func (s *Scheduler) Query() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("input", "Query now=%d", s.clock.Now())
	s.expireLocked("query", strconv.FormatInt(s.clock.Now(), 10))
	snap := s.snapshotLocked()
	s.logf("output", "Query %s", s.stateSummary())
	return snap
}

// scheduleLocked 是唯一的调度入口，必须在持有 s.mu 时调用，全过程确定可复现。
func (s *Scheduler) scheduleLocked(reason string) {
	now := s.clock.Now()
	s.expireLocked("schedule:"+reason, strconv.FormatInt(now, 10))

	// 1) 按提交顺序启动所有“现在立即放得下”的队首作业。
	for len(s.queued) > 0 {
		head := s.queued[0]
		if head.nodes > s.freeNodesLocked() {
			break
		}
		s.launchLocked(head, now, "immediate-head")
		s.queued = s.queued[1:]
	}
	if len(s.queued) == 0 {
		return
	}

	// 2) 为队首计算影子时刻与富余数：按预估结束时刻升序（并列按作业标识）
	//    逐个“释放”运行作业，直到累计可用节点满足队首需求。
	head := s.queued[0]
	type release struct {
		end int64
		id  string
	}
	events := make([]release, 0, len(s.running))
	used := int64(0)
	for _, r := range s.running {
		events = append(events, release{end: r.end, id: r.id})
		used += r.nodes
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].end != events[j].end {
			return events[i].end < events[j].end
		}
		return events[i].id < events[j].id
	})
	available := s.nodes - used
	shadow := now
	idx := 0
	for available < head.nodes && idx < len(events) {
		shadow = events[idx].end
		// 释放同一时刻结束的整组作业（组内已按标识排序）。
		for idx < len(events) && events[idx].end == shadow {
			available += s.running[events[idx].id].nodes
			idx++
		}
	}
	surplus := available - head.nodes
	head.shadow, head.surplus = shadow, surplus
	s.logf("reserve", "head=%q need=%d free=%d shadow=%d surplus=%d events=%d reason=%s",
		head.id, head.nodes, s.nodes-used, shadow, surplus, len(events), reason)

	// 3) 按提交顺序检查其余作业能否回填。回填作业从队列中移出但队首保留原位。
	remaining := make([]*jobState, 0, len(s.queued))
	remaining = append(remaining, head)
	free := s.freeNodesLocked()
	for _, b := range s.queued[1:] {
		fitNow := b.nodes <= free
		short := fitNow && now+b.duration <= shadow
		long := fitNow && !short && b.nodes <= surplus
		switch {
		case short:
			s.launchLocked(b, now, "backfill-short end<="+strconv.FormatInt(shadow, 10))
			free -= b.nodes
		case long:
			s.launchLocked(b, now, "backfill-long within-surplus")
			free -= b.nodes
			surplus -= b.nodes
		default:
			s.logf("backfill-skip", "id=%q need=%d free=%d fitNow=%t end=%d shadow=%d surplus=%d",
				b.id, b.nodes, free, fitNow, now+b.duration, shadow, surplus)
			remaining = append(remaining, b)
		}
	}
	s.queued = remaining
}

// expireLocked 强制结束所有预估结束时刻不晚于当前时钟的作业，顺序确定。
func (s *Scheduler) expireLocked(reason, nowText string) {
	now := s.clock.Now()
	due := make([]*jobState, 0)
	for _, r := range s.running {
		if r.end <= now {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].end != due[j].end {
			return due[i].end < due[j].end
		}
		return due[i].id < due[j].id
	})
	for _, r := range due {
		if _, ok := s.running[r.id]; !ok {
			continue
		}
		s.removeRunningLocked(r)
		s.logf("event", "id=%q forced-end start=%d end=%d at=%s reason=%s released=%d",
			r.id, r.start, r.end, nowText, reason, r.nodes)
	}
}

func (s *Scheduler) launchLocked(j *jobState, now int64, why string) {
	j.start, j.end, j.shadow, j.surplus = now, now+j.duration, 0, 0
	s.running[j.id] = j
	s.logf("event", "id=%q launch start=%d end=%d nodes=%d why=%s freeLeft=%d",
		j.id, now, j.end, j.nodes, why, s.freeNodesLocked())
}

func (s *Scheduler) removeRunningLocked(j *jobState) { delete(s.running, j.id) }

func (s *Scheduler) freeNodesLocked() int64 {
	used := int64(0)
	for _, r := range s.running {
		used += r.nodes
	}
	return s.nodes - used
}

func (s *Scheduler) snapshotLocked() Snapshot {
	running := make([]RunningJob, 0, len(s.running))
	for _, r := range s.running {
		running = append(running, RunningJob{ID: r.id, Nodes: r.nodes, StartTime: r.start, EndTime: r.end})
	}
	sort.Slice(running, func(i, j int) bool {
		if running[i].StartTime != running[j].StartTime {
			return running[i].StartTime < running[j].StartTime
		}
		return running[i].ID < running[j].ID
	})
	queued := make([]QueueEntry, 0, len(s.queued))
	for _, q := range s.queued {
		queued = append(queued, QueueEntry{
			ID: q.id, Nodes: q.nodes, Duration: q.duration,
			Shadow: q.shadow, Surplus: q.surplus,
		})
	}
	return Snapshot{Now: s.clock.Now(), Running: running, Queue: queued}
}

func (s *Scheduler) stateSummary() string {
	used := int64(0)
	for _, r := range s.running {
		used += r.nodes
	}
	return "running=" + strconv.Itoa(len(s.running)) +
		"/used=" + strconv.FormatInt(used, 10) +
		"/queue=" + strconv.Itoa(len(s.queued))
}

func (s *Scheduler) logf(tag, format string, args ...any) {
	if s.logW == nil {
		return
	}
	io.WriteString(s.logW, "[scheduler] "+tag+" ")
	fmt.Fprintf(s.logW, format+"\n", args...)
}
