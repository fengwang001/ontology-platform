package saga

import (
	_ "unsafe"

	"ontology/journal"
	"ontology/step"
)

// resumeReadCount 仅供同模块测试/demo 通过 go:linkname 成对链接读取非导出计数器，
// 不出现在任何公开接口中。

//go:linkname resumeReadCount ontology/saga.intrnlResumeReadCount
func resumeReadCount(o *Orchestrator) int { return o.resumeReads }

//go:linkname intrnlJournal ontology/saga.intrnlJournal
func intrnlJournal(o *Orchestrator) *journal.Journal { return o.journal }

//go:linkname intrnlAppend ontology/saga.intrnlAppend
func intrnlAppend(o *Orchestrator, id string, idx int, key string,
	dir journal.Direction, res journal.Result) (journal.Record, error) {
	return o.journal.Append(journal.Record{
		InstanceID: id, StepIndex: idx, StepKey: key,
		Direction: dir, Result: res, At: o.now(),
	})
}

//go:linkname intrnlRegister ontology/saga.intrnlRegister
func intrnlRegister(o *Orchestrator, id string, steps []step.Step) {
	it := &inst{
		steps: steps,
		calls: Calls{Forward: map[string]int{}, Compensate: map[string]int{}},
	}
	o.register(id, it)
	it.cached = reconstruct(id, o.journal.Read(id), len(steps))
}
