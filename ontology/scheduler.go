package ontology

import (
	"context"
	"fmt"
)

// EventKind 标识调度事件类型。
type EventKind int

const (
	EventRead EventKind = iota
	EventVerdict
)

func (k EventKind) String() string {
	if k == EventRead {
		return "read"
	}
	return "verdict"
}

// SchedEvent 完整刻画一次尝试的读基线与判定，事件流用于重放核验。
type SchedEvent struct {
	Kind     EventKind
	OpID     int
	Attempt  int
	Verdict  verdict
	Base     Baseline
	Terminal bool
}

type gateReq struct {
	opID    int
	attempt int
	base    Baseline
	perm    chan struct{}
}

// Scheduler 是 SyncHooks 的确定性实现，提供两道闸门：
//   - 读闸门（BeforeRead）：决定哪些尝试可以读取基线；
//   - 提交闸门（BeforeCommit）：已读完基线、在途停放的尝试在此等待
//     CAS 放行。脚本据此精确控制“谁读到哪个版本、谁先生效”。
type Scheduler struct {
	readCh chan gateReq
	commCh chan gateReq
	doneCh chan AttemptRecord

	events   []SchedEvent
	executor *Executor
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		readCh: make(chan gateReq),
		commCh: make(chan gateReq),
		doneCh: make(chan AttemptRecord, 1024),
	}
}

func (s *Scheduler) BeforeRead(opID, attempt int) {
	r := gateReq{opID: opID, attempt: attempt, perm: make(chan struct{})}
	s.readCh <- r
	<-r.perm
}

func (s *Scheduler) BeforeCommit(opID, attempt int, base Baseline) {
	r := gateReq{opID: opID, attempt: attempt, base: base, perm: make(chan struct{})}
	// 在读到基线、进入提交闸门时立即记录读事件，使事件流保持
	// “先读后判”的真实顺序，供串行参照模型重放。
	s.events = append(s.events, SchedEvent{
		Kind:    EventRead,
		OpID:    opID,
		Attempt: attempt,
		Base:    base,
	})
	s.commCh <- r
	<-r.perm
}

func (s *Scheduler) AfterCommit(rec AttemptRecord) {
	s.events = append(s.events, SchedEvent{
		Kind:     EventVerdict,
		OpID:     rec.OpID,
		Attempt:  rec.Attempt,
		Verdict:  parseVerdictSafe(rec.Verdict),
		Base:     Baseline{Version: rec.BaseVersion, HighWater: rec.BaseHighWater},
		Terminal: rec.Verdict != "conflict",
	})
	s.doneCh <- rec
}

func (s *Scheduler) Events() []SchedEvent { return append([]SchedEvent(nil), s.events...) }

// Step 是脚本中的一步：放行某类闸门上指定 opID 的尝试。
type Step struct {
	OpID   int
	Commit bool // false=读闸门, true=提交闸门
}

// Run 确定性执行全部动作。脚本按 Step 顺序放行：
// 读闸门放行后动作立即读快照并进入提交闸门；提交闸门放行后执行 CAS。
// 因此“连续多个读步骤”让这些动作读到同一版本（其间无 CAS），
// 随后的提交步骤决定 CAS 生效顺序。
func Run(ops []Op, steps []Step) (map[int]OpResult, *Scheduler) {
	s := NewScheduler()
	exec := NewExecutor(s)
	s.executor = exec

	results := make(chan OpResult, len(ops))
	for _, op := range ops {
		op := op
		go func() { results <- exec.Run(context.Background(), op) }()
	}

	out := make(map[int]OpResult, len(ops))
	var readQ []gateReq
	var commQ []gateReq
	stepIdx := 0

	findAndRelease := func(q *[]gateReq, opID int) (gateReq, bool) {
		for i, r := range *q {
			if r.opID == opID {
				*q = append((*q)[:i], (*q)[i+1:]...)
				close(r.perm)
				return r, true
			}
		}
		return gateReq{}, false
	}

	for len(out) < len(ops) {
		// 排空当前已就绪的闸门请求与终结结果。
		drained := false
		for !drained {
			select {
			case r := <-s.readCh:
				readQ = append(readQ, r)
			case r := <-s.commCh:
				commQ = append(commQ, r)
			case res := <-results:
				out[res.OpID] = res
			default:
				drained = true
			}
		}
		if len(out) == len(ops) {
			break
		}

		if stepIdx < len(steps) {
			st := steps[stepIdx]
			var q *[]gateReq
			if st.Commit {
				q = &commQ
			} else {
				q = &readQ
			}
			if r, ok := findAndRelease(q, st.OpID); ok {
				stepIdx++
				if st.Commit {
					s.awaitDone(r.opID, r.attempt, results, &readQ, &commQ, out)
				} else {
					// 等待该尝试读完快照并停到提交闸门，保证脚本的
					// 下一个读/提交步骤看到确定状态。
					s.awaitCommitReady(r.opID, r.attempt, results, &readQ, &commQ, out)
				}
				continue
			}
			// 目标尚未到闸门：等待新到达（或终结）。
			s.waitOne(results, &readQ, &commQ, out)
			continue
		}

		// 脚本耗尽：FIFO 放行剩余尝试直至全部终结。
		if len(commQ) > 0 {
			r := commQ[0]
			commQ = commQ[1:]
			close(r.perm)
			s.awaitDone(r.opID, r.attempt, results, &readQ, &commQ, out)
			continue
		}
		if len(readQ) > 0 {
			r := readQ[0]
			readQ = readQ[1:]
			close(r.perm)
			continue
		}
		s.waitOne(results, &readQ, &commQ, out)
	}
	return out, s
}

func (s *Scheduler) waitOne(results <-chan OpResult, readQ, commQ *[]gateReq, out map[int]OpResult) {
	select {
	case r := <-s.readCh:
		*readQ = append(*readQ, r)
	case r := <-s.commCh:
		*commQ = append(*commQ, r)
	case res := <-results:
		out[res.OpID] = res
	case rec := <-s.doneCh:
		_ = rec
	}
}

func (s *Scheduler) awaitDone(opID int, attempt int, results <-chan OpResult, readQ, commQ *[]gateReq, out map[int]OpResult) {
	for {
		select {
		case rec := <-s.doneCh:
			if rec.OpID == opID && rec.Attempt == attempt {
				return
			}
		case r := <-s.readCh:
			*readQ = append(*readQ, r)
		case r := <-s.commCh:
			*commQ = append(*commQ, r)
		case res := <-results:
			out[res.OpID] = res
		}
	}
}

// awaitCommitReady 等待指定尝试出现在提交闸门（已读完基线），
// 期间回收其他到达与终结结果。
func (s *Scheduler) awaitCommitReady(opID int, attempt int, results <-chan OpResult, readQ, commQ *[]gateReq, out map[int]OpResult) {
	for {
		for _, r := range *commQ {
			if r.opID == opID && r.attempt == attempt {
				return
			}
		}
		// 该动作可能已经终结（例如读阶段无需 CAS 的极端情况不存在，
		// 但仍兼容结果先到的调度）。
		if _, done := out[opID]; done {
			return
		}
		select {
		case r := <-s.readCh:
			*readQ = append(*readQ, r)
		case r := <-s.commCh:
			*commQ = append(*commQ, r)
		case res := <-results:
			out[res.OpID] = res
		}
	}
}

func parseVerdictSafe(name string) verdict {
	switch name {
	case "committed":
		return verdictCommitted
	case "preempted":
		return verdictPreempted
	case "conflict":
		return verdictConflict
	default:
		return -1
	}
}

func (ev SchedEvent) String() string {
	if ev.Kind == EventRead {
		return fmt.Sprintf("read(op=%d,attempt=%d,v=%d)", ev.OpID, ev.Attempt, ev.Base.Version)
	}
	return fmt.Sprintf("verdict(op=%d,attempt=%d,%s)", ev.OpID, ev.Attempt, verdictName(ev.Verdict))
}
