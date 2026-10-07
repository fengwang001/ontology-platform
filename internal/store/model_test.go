package store

// naiveModel is an independent, deliberately simple full-replay reference
// implementation. It models the exact specification:
//
//   - a crash at a barrier strictly before the commit point means the
//     batch never happened;
//   - a crash at or after the commit point means the batch fully happened;
//   - recovery is idempotent and never applies a batch twice;
//   - a normal abort means the batch never happened.
//
// The production Store reaches the same final state via intents and a
// state machine; the tests compare the two implementations over many
// randomly generated schedules.

type naiveInstance struct {
	version int64
	props   map[string]string
}

type naiveModel struct {
	instances map[string]*naiveInstance
	committed map[string]bool
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		instances: make(map[string]*naiveInstance),
		committed: make(map[string]bool),
	}
}

func (m *naiveModel) seed(id string, v int64, props map[string]string) {
	p := make(map[string]string, len(props))
	for k, val := range props {
		p[k] = val
	}
	m.instances[id] = &naiveInstance{version: v, props: p}
}

// classifyBeforeCommit answers the specification question for a crash that
// occurs at the given barrier, where isCommitPoint reports whether that
// barrier is the unique externally-visible decision instant.
func classifyCrash(barrier string, isCommitPoint bool) (committed bool) {
	return isCommitPoint
}

// applyCommit is the naive whole-batch effect used as the comparison
// target for "fully effective".
func (m *naiveModel) applyCommit(ops []Op) {
	for _, op := range ops {
		inst, ok := m.instances[op.ObjectID]
		if !ok {
			inst = &naiveInstance{props: map[string]string{}}
			m.instances[op.ObjectID] = inst
		}
		p := make(map[string]string, len(op.Properties))
		for k, v := range op.Properties {
			p[k] = v
		}
		inst.props = p
		inst.version++
	}
}

func (m *naiveModel) markCommitted(batchID string) { m.committed[batchID] = true }
