package worker

import "sort"

// Worker 是一个工作者：属性、槽位与当前持有的操作。
type Worker struct {
	Name  string
	Props map[string]string
	slots int
	used  int
	held  map[string]bool
}

// New 创建工作者；slots 合法性（1..64）由 exec 层在注册前校验。
func New(name string, props map[string]string, slots int) *Worker {
	copied := make(map[string]string, len(props))
	for k, v := range props {
		copied[k] = v
	}
	return &Worker{
		Name:  name,
		Props: copied,
		slots: slots,
		held:  map[string]bool{},
	}
}

func (w *Worker) Slots() int               { return w.slots }
func (w *Worker) Used() int                { return w.used }
func (w *Worker) FreeSlots() int           { return w.slots - w.used }
func (w *Worker) Holds(digest string) bool { return w.held[digest] }

// Acquire 占用一个槽登记持有操作；调用方须先确认有空槽且未重复持有。
func (w *Worker) Acquire(digest string) {
	w.held[digest] = true
	w.used++
}

// Release 释放一个槽；重复释放是调用方编程错误。
func (w *Worker) Release(digest string) {
	delete(w.held, digest)
	w.used--
}

// HeldDigests 按字典序返回持有的操作摘要，供 WorkerLost 时按 seq 排序前取稳定快照。
func (w *Worker) HeldDigests() []string {
	out := make([]string, 0, len(w.held))
	for d := range w.held {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
