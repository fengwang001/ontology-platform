package history

import "sync"

// MaxEventsPerWorkflow 是单个工作流允许保存的事件数上限。
const MaxEventsPerWorkflow = 100_000

// Store 是按工作流隔离的追加式事件日志存储。
// 不同工作流之间使用不同的内部锁，可并发读写；同一工作流的
// Append 在锁内完成“校验长度—检查容量—写入”，保证整批原子。
//
// Store 的零值即可使用，复制 Store 没有意义（内部含互斥锁）。
type Store struct {
	mu sync.Map // wfKey(string) -> *workflowLog
}

// wfKey 把工作流字节串转为 map 可用的稳定键（零拷贝字符串）。
// 调用方不再持有同一底层数组的可变引用：wfs 来自 Run 的快照期参数，
// 这里转换后仅在锁内作为 key 使用，不与外部共享。
type wfKey string

type workflowLog struct {
	mu     sync.Mutex
	events []Event
}

func (s *Store) logFor(wf []byte) *workflowLog {
	key := wfKey(wf)
	actual, _ := s.mu.LoadOrStore(key, &workflowLog{})
	return actual.(*workflowLog)
}

// Append 把一整批 events 原子追加到工作流 wf 之后。
//
// 拒绝次序（任一拒绝都整批不写）：
//  1. 参数非法（wf 为空、事件中的 name/pid 为空、事件种类未知）；
//     空批次视为成功的无操作，且不校验任何参数；
//  2. expect 不等于日志当前长度：ErrConflict；
//  3. 追加后长度超过 MaxEventsPerWorkflow：ErrCapacity。
func (s *Store) Append(wf []byte, expect int, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := validateBatch(wf, events); err != nil {
		return err
	}

	log := s.logFor(wf)
	log.mu.Lock()
	defer log.mu.Unlock()

	if len(log.events) != expect {
		return ErrConflict
	}
	if len(log.events)+len(events) > MaxEventsPerWorkflow {
		return ErrCapacity
	}

	batch := make([]Event, len(events))
	for i, e := range events {
		batch[i] = e.clone()
	}
	log.events = append(log.events, batch...)
	return nil
}

// Snapshot 返回工作流 wf 当前全部事件的深拷贝。
// 修改返回切片或其中事件的字节串都不会影响日志内部状态。
// 未知（或尚无事件）的工作流返回非 nil 的空切片。
func (s *Store) Snapshot(wf []byte) []Event {
	if len(wf) == 0 {
		return []Event{}
	}
	value, ok := s.mu.Load(wfKey(wf))
	if !ok {
		return []Event{}
	}
	log := value.(*workflowLog)
	log.mu.Lock()
	defer log.mu.Unlock()

	out := make([]Event, len(log.events))
	for i, e := range log.events {
		out[i] = e.clone()
	}
	return out
}

// Len 返回工作流 wf 当前的事件数（主要供测试与观测使用）。
func (s *Store) Len(wf []byte) int {
	if len(wf) == 0 {
		return 0
	}
	value, ok := s.mu.Load(wfKey(wf))
	if !ok {
		return 0
	}
	log := value.(*workflowLog)
	log.mu.Lock()
	defer log.mu.Unlock()
	return len(value.(*workflowLog).events)
}
