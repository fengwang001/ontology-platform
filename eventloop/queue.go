package eventloop

import "fmt"

// task 是一个待执行任务，携带任务源与入队时刻。
type task struct {
	handle      Handle
	source      Source
	enqueueTime int64
	note        string // 进入轨迹 Reason 的来源说明（如定时器到期）
	cb          func()
	cancelled   bool
}

// sourceQueue 是单源 FIFO 队列；skips 记录当前队首被跳过的轮次数。
type sourceQueue struct {
	tasks []*task
	skips int
}

// scheduler 跨源选择下一个任务。源的数量固定为 5，
// 每次选择只检查各源队首，开销与待执行任务总数无关（O(1)）。
type scheduler struct {
	queues [numSources]sourceQueue
	limit  int
	stats  *Stats
}

func (s *scheduler) enqueue(t *task) {
	q := &s.queues[t.source]
	q.tasks = append(q.tasks, t)
}

// discardCancelledHeads 静默丢弃各源队首的已取消任务。
// 被取消的任务不占饥饿轮次、也不算被跳过，因此这里不增加任何计数。
func (s *scheduler) discardCancelledHeads() {
	for i := range s.queues {
		q := &s.queues[i]
		for len(q.tasks) > 0 && q.tasks[0].cancelled {
			q.tasks = q.tasks[1:]
			q.skips = 0
		}
	}
}

// hasRunnable 报告是否存在可运行任务（调用前须先 discardCancelledHeads）。
func (s *scheduler) hasRunnable() bool {
	for i := range s.queues {
		if len(s.queues[i].tasks) > 0 {
			return true
		}
	}
	return false
}

// lessHead 是跨源并列打破规则：入队时刻最早者优先，仍并列按固定源次序。
func lessHead(a *task, asrc int, b *task, bsrc int) bool {
	if a.enqueueTime != b.enqueueTime {
		return a.enqueueTime < b.enqueueTime
	}
	return asrc < bsrc
}

// selectNext 选择下一个待执行任务，返回任务与判定依据。
// 规则优先级：饥饿强制 > 用户交互优先 > 入队时刻最早。
// 调用前须先 discardCancelledHeads。
func (s *scheduler) selectNext() (*task, string) {
	chosen := -1
	reason := ""

	// 1. 饥饿强制：队首被跳过轮次达到上限的源必须被选中；
	//    多个源同时到达上限时取入队时刻最早者，仍并列按固定源次序。
	for i := 0; i < numSources; i++ {
		q := &s.queues[i]
		if len(q.tasks) == 0 {
			continue
		}
		s.stats.HeadChecks++
		if q.skips >= s.limit && (chosen < 0 || lessHead(q.tasks[0], i, s.queues[chosen].tasks[0], chosen)) {
			chosen = i
		}
	}
	if chosen >= 0 {
		reason = fmt.Sprintf("starvation limit %d reached for %s", s.limit, Source(chosen))
	} else if len(s.queues[SourceUserInteraction].tasks) > 0 {
		// 2. 用户交互源有待执行任务时相对其他源优先。
		s.stats.HeadChecks++
		chosen = int(SourceUserInteraction)
		reason = "user-interaction priority"
	} else {
		// 3. 默认规则：入队时刻最早者，并列按固定源次序。
		for i := 0; i < numSources; i++ {
			q := &s.queues[i]
			if len(q.tasks) == 0 {
				continue
			}
			s.stats.HeadChecks++
			if chosen < 0 || lessHead(q.tasks[0], i, s.queues[chosen].tasks[0], chosen) {
				chosen = i
			}
		}
		if chosen >= 0 {
			reason = "earliest enqueue time"
		}
	}

	if chosen < 0 {
		return nil, ""
	}
	s.stats.Selections++
	q := &s.queues[chosen]
	t := q.tasks[0]
	q.tasks = q.tasks[1:]
	q.skips = 0
	// 其余有待执行队首的源本轮被跳过，队首饥饿计数加一。
	for i := 0; i < numSources; i++ {
		if i != chosen && len(s.queues[i].tasks) > 0 {
			s.queues[i].skips++
		}
	}
	return t, reason
}
