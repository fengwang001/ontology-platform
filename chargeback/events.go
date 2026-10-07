package chargeback

import "container/heap"

// eventKind 区分两类到期自动认定事件。
type eventKind int

const (
	eventForfeit    eventKind = iota // 应诉期逾期：商户放弃，发卡行胜
	eventAutoAccept                  // 审阅期逾期：视为接受应诉，商户胜
)

// scheduledEvent 是一笔到期资金事件的预约。事件只在被处理时检查
// 案件当前记录是否仍然满足条件（惰性删除），因此无需取消机制。
type scheduledEvent struct {
	day    int
	seq    int64 // 同日的 tie-break，保证堆顺序确定
	kind   eventKind
	caseID string
}

// eventHeap 是按 (day, seq) 排序的最小堆。
type eventHeap []scheduledEvent

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].day != h[j].day {
		return h[i].day < h[j].day
	}
	return h[i].seq < h[j].seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) { *h = append(*h, x.(scheduledEvent)) }

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// scheduler 管理到期事件，支持“收集-提交/回滚”两阶段语义：
// 被拒绝的操作不得改变资金，因此先收集到期事件计算差额，
// 仅在操作被接受时才提交，被拒绝则原样放回。
type scheduler struct {
	h   eventHeap
	seq int64
}

func newScheduler() *scheduler {
	return &scheduler{}
}

func (s *scheduler) schedule(day int, kind eventKind, caseID string) {
	heap.Push(&s.h, scheduledEvent{day: day, seq: s.seq, kind: kind, caseID: caseID})
	s.seq++
}

// collectDue 弹出所有 day <= now 的事件；调用方负责 commit 或 rollback。
func (s *scheduler) collectDue(now int) []scheduledEvent {
	var due []scheduledEvent
	for len(s.h) > 0 && s.h[0].day <= now {
		due = append(due, heap.Pop(&s.h).(scheduledEvent))
	}
	return due
}

// rollback 把已收集的事件原样放回（用于被拒绝的操作）。
func (s *scheduler) rollback(due []scheduledEvent) {
	for _, e := range due {
		heap.Push(&s.h, e)
	}
}

// pendingEvents 返回堆中尚未触发的事件数（测试与调试用）。
func (s *scheduler) pendingEvents() int { return len(s.h) }
